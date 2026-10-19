package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// Engine owns the encrypted DB and the WhatsApp connection.
type Engine struct {
	vault *Vault
	db    *sql.DB
	st    *Store
	cli   *whatsmeow.Client

	mu     sync.Mutex
	subs   map[int]chan Event
	nextID int

	presMu sync.Mutex
	pres   map[string]PresenceInfo

	connected atomic.Bool
	active    atomic.Value // string: chat currently open in a TUI (no notifications for it)
	pairOK    chan struct{}
	stop      chan struct{}
	closeOnce sync.Once
}

func NewEngine(pass string) (*Engine, error) {
	v, err := OpenVault(pass)
	if err != nil {
		return nil, err
	}
	fail := func(db *sql.DB, err error) (*Engine, error) {
		if db != nil {
			db.Close()
		}
		v.release()
		return nil, err
	}
	db, err := openDB(v.runPath)
	if err != nil {
		return fail(nil, err)
	}
	st := &Store{db: db}
	if err := st.init(); err != nil {
		return fail(db, err)
	}

	var logger waLog.Logger = waLog.Noop
	if os.Getenv("WACLI_DEBUG") != "" {
		logger = waLog.Stdout("wa", "DEBUG", true)
	}
	ctx := context.Background()
	container := sqlstore.NewWithDB(db, "sqlite3", logger)
	if err := container.Upgrade(ctx); err != nil {
		return fail(db, err)
	}
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fail(db, err)
	}
	cli := whatsmeow.NewClient(dev, logger)

	e := &Engine{
		vault:  v,
		db:     db,
		st:     st,
		cli:    cli,
		subs:   map[int]chan Event{},
		pres:   map[string]PresenceInfo{},
		pairOK: make(chan struct{}, 1),
		stop:   make(chan struct{}),
	}
	e.active.Store("")
	cli.AddEventHandler(e.handleEvent)
	go e.sealLoop()
	return e, nil
}

func (e *Engine) sealLoop() {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := e.vault.Seal(e.db); err != nil {
				log.Printf("seal: %v", err)
			}
		case <-e.stop:
			return
		}
	}
}

func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		close(e.stop)
		e.cli.Disconnect()
		e.vault.Close(e.db)
	})
	return nil
}

func (e *Engine) LoggedIn() bool { return e.cli.Store.ID != nil }

func (e *Engine) Connect() error {
	if !e.LoggedIn() {
		return errNotLinked
	}
	return e.cli.Connect()
}

func (e *Engine) WaitConnected(d time.Duration) error {
	deadline := time.After(d)
	for {
		if e.connected.Load() {
			return nil
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			return errors.New("could not connect to WhatsApp (offline?)")
		}
	}
}

func (e *Engine) ready() error {
	if !e.LoggedIn() {
		return errNotLinked
	}
	if !e.connected.Load() {
		return errors.New("not connected to WhatsApp right now")
	}
	return nil
}

func (e *Engine) meJID() string {
	if e.cli.Store.ID == nil {
		return ""
	}
	return e.cli.Store.ID.ToNonAD().String()
}

// PairWithPhone links this client using a pairing code typed on the phone.
func (e *Engine) PairWithPhone(phone string, show func(code string)) error {
	if err := e.cli.Connect(); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	code, err := e.cli.PairPhone(context.Background(), phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return err
	}
	show(code)
	select {
	case <-e.pairOK:
	case <-time.After(3 * time.Minute):
		return errors.New("timed out waiting for the code to be entered")
	}
	return e.WaitConnected(60 * time.Second)
}

func (e *Engine) Logout() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return e.cli.Logout(ctx)
}

// ---------------------------------------------------------------- events

func (e *Engine) emit(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ch := range e.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (e *Engine) Subscribe() (<-chan Event, func(), error) {
	ch := make(chan Event, 128)
	e.mu.Lock()
	id := e.nextID
	e.nextID++
	e.subs[id] = ch
	e.mu.Unlock()
	cancel := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if _, ok := e.subs[id]; ok {
			delete(e.subs, id)
			close(ch)
		}
	}
	return ch, cancel, nil
}

func (e *Engine) handleEvent(raw interface{}) {
	switch evt := raw.(type) {
	case *events.Connected:
		e.connected.Store(true)
		go e.syncNames()
		e.emit(Event{Type: "connected"})
	case *events.Disconnected:
		e.connected.Store(false)
		e.emit(Event{Type: "disconnected"})
	case *events.LoggedOut:
		e.connected.Store(false)
		e.emit(Event{Type: "disconnected"})
	case *events.PairSuccess:
		select {
		case e.pairOK <- struct{}{}:
		default:
		}
	case *events.Message:
		e.onMessage(evt)
	case *events.Receipt:
		e.onReceipt(evt)
	case *events.Presence:
		jid := evt.From.ToNonAD().String()
		p := PresenceInfo{Known: true, Online: !evt.Unavailable}
		if !evt.LastSeen.IsZero() {
			p.LastSeen = evt.LastSeen.Unix()
		}
		e.presMu.Lock()
		e.pres[jid] = p
		e.presMu.Unlock()
		e.emit(Event{Type: "presence", Chat: jid, On: p.Online})
	case *events.ChatPresence:
		e.emit(Event{
			Type: "typing",
			Chat: evt.Chat.ToNonAD().String(),
			From: evt.Sender.ToNonAD().String(),
			On:   evt.State == types.ChatPresenceComposing,
		})
	case *events.PushName:
		e.st.setName(evt.JID.ToNonAD().String(), evt.NewPushName, 1)
	}
}

// normChat maps LID chats to phone-number chats when WhatsApp tells us both.
// (If your whatsmeow version has no SenderAlt/RecipientAlt, delete the body of the `if`.)
func normChat(info types.MessageInfo) types.JID {
	chat := info.Chat.ToNonAD()
	if !info.IsGroup && chat.Server == types.HiddenUserServer {
		alt := info.RecipientAlt
		if !info.IsFromMe {
			alt = info.SenderAlt
		}
		if !alt.IsEmpty() {
			return alt.ToNonAD()
		}
	}
	return chat
}

func (e *Engine) onMessage(evt *events.Message) {
	info := evt.Info
	chat := normChat(info).String()
	sender := info.Sender.ToNonAD().String()
	if info.IsFromMe {
		sender = e.meJID()
	} else if !info.IsGroup {
		sender = chat
	}
	msg := evt.Message

	if pm := msg.GetProtocolMessage(); pm != nil {
		switch pm.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			e.st.markDeleted(chat, pm.GetKey().GetID())
			e.emit(Event{Type: "update", Chat: chat})
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			if em := pm.GetEditedMessage(); em != nil {
				t, _, _ := describe(em)
				e.st.markEdited(chat, pm.GetKey().GetID(), t)
				e.emit(Event{Type: "update", Chat: chat})
			}
		}
		return
	}
	if rm := msg.GetReactionMessage(); rm != nil {
		e.st.setReaction(chat, rm.GetKey().GetID(), sender, rm.GetText())
		e.emit(Event{Type: "update", Chat: chat})
		return
	}

	if info.PushName != "" && !info.IsFromMe {
		e.st.setName(sender, info.PushName, 1)
	}
	text, kind, fname := describe(msg)
	if kind == "" {
		return
	}
	replyTo := ""
	if ci := ctxInfo(msg); ci != nil {
		replyTo = ci.GetStanzaID()
	}
	m := &Message{
		Chat:       chat,
		ID:         string(info.ID),
		Sender:     sender,
		SenderName: info.PushName,
		FromMe:     info.IsFromMe,
		TS:         info.Timestamp.Unix(),
		Text:       text,
		Kind:       kind,
		Filename:   fname,
		ReplyTo:    replyTo,
	}
	if info.IsFromMe {
		m.SenderName = ""
	}
	var rawMsg []byte
	if kind != "text" && kind != "location" {
		rawMsg, _ = proto.Marshal(msg)
	}
	if err := e.st.insertMessage(m, rawMsg); err != nil {
		log.Printf("store message: %v", err)
		return
	}
	unread := 0
	if !info.IsFromMe {
		unread = 1
	}
	_ = e.st.touchChat(chat, info.IsGroup, m.TS, previewOf(m), unread)
	if n := e.st.name(sender); n != "" {
		m.SenderName = n
	}
	e.emit(Event{Type: "message", Chat: chat, Msg: m})
	if !info.IsFromMe {
		e.maybeNotify(chat, m)
	}
}

func (e *Engine) maybeNotify(chat string, m *Message) {
	if !loadConfig().Notifications {
		return
	}
	if a, _ := e.active.Load().(string); a == chat {
		return
	}
	title := e.st.name(chat)
	if title == "" {
		title = firstNonEmpty(m.SenderName, userPart(chat))
	}
	body := previewOf(m)
	if strings.HasSuffix(chat, "@g.us") && m.SenderName != "" {
		body = m.SenderName + ": " + body
	}
	notify(chat, title, body)
}

func (e *Engine) onReceipt(evt *events.Receipt) {
	chat := evt.Chat.ToNonAD().String()
	if evt.Type == types.ReceiptTypeReadSelf {
		e.st.clearUnread(chat)
		e.emit(Event{Type: "update", Chat: chat})
		return
	}
	status := 0
	switch evt.Type {
	case types.ReceiptTypeDelivered:
		status = 1
	case types.ReceiptTypeRead, types.ReceiptTypePlayed:
		status = 2
	default:
		return
	}
	ids := make([]string, 0, len(evt.MessageIDs))
	for _, id := range evt.MessageIDs {
		ids = append(ids, string(id))
	}
	e.st.markStatus(ids, status)
	e.emit(Event{Type: "update", Chat: chat})
}

func (e *Engine) syncNames() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if all, err := e.cli.Store.Contacts.GetAllContacts(ctx); err == nil {
		for jid, c := range all {
			n := firstNonEmpty(c.FullName, c.FirstName, c.BusinessName)
			if n != "" {
				e.st.setName(jid.ToNonAD().String(), n, 2)
			} else if c.PushName != "" {
				e.st.setName(jid.ToNonAD().String(), c.PushName, 1)
			}
		}
	}
	if gs, err := e.cli.GetJoinedGroups(ctx); err == nil {
		for _, g := range gs {
			e.st.setName(g.JID.String(), g.Name, 3)
		}
	}
	e.emit(Event{Type: "update"})
}

// ---------------------------------------------------------------- message helpers

func describe(m *waE2E.Message) (text, kind, fname string) {
	switch {
	case m == nil:
		return "", "", ""
	case m.GetConversation() != "":
		return m.GetConversation(), "text", ""
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetText(), "text", ""
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetCaption(), "image", ""
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetCaption(), "video", ""
	case m.GetAudioMessage() != nil:
		return "", "audio", ""
	case m.GetDocumentMessage() != nil:
		d := m.GetDocumentMessage()
		return d.GetCaption(), "document", d.GetFileName()
	case m.GetStickerMessage() != nil:
		return "", "sticker", ""
	case m.GetLocationMessage() != nil:
		l := m.GetLocationMessage()
		return fmt.Sprintf("%.5f,%.5f", l.GetDegreesLatitude(), l.GetDegreesLongitude()), "location", ""
	}
	return "", "", ""
}

func ctxInfo(m *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetContextInfo()
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetContextInfo()
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetContextInfo()
	case m.GetAudioMessage() != nil:
		return m.GetAudioMessage().GetContextInfo()
	case m.GetDocumentMessage() != nil:
		return m.GetDocumentMessage().GetContextInfo()
	}
	return nil
}

func previewOf(m *Message) string {
	if m.Kind == "text" {
		return m.Text
	}
	label := "[" + m.Kind + "]"
	if m.Filename != "" {
		label = "[" + m.Filename + "]"
	}
	return strings.TrimSpace(label + " " + m.Text)
}

// ---------------------------------------------------------------- Backend API

func (e *Engine) Status() (Status, error) {
	s := Status{LoggedIn: e.LoggedIn(), Connected: e.connected.Load(), Mode: "local", PushName: e.cli.Store.PushName}
	if e.cli.Store.ID != nil {
		s.Phone = e.cli.Store.ID.User
	}
	return s, nil
}

func (e *Engine) Chats() ([]Chat, error) { return e.st.chats() }

func (e *Engine) Messages(q MsgQuery) ([]Message, error) {
	jid, err := e.Resolve(q.Chat)
	if err != nil {
		return nil, err
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	return e.st.messages(jid, q.Limit)
}

func (e *Engine) Contacts() ([]Contact, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	all, err := e.cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Contact, 0, len(all))
	for jid, c := range all {
		ct := Contact{
			JID:  jid.ToNonAD().String(),
			Name: firstNonEmpty(c.FullName, c.FirstName, c.BusinessName, c.PushName),
		}
		if jid.Server == types.DefaultUserServer {
			ct.Phone = jid.User
		}
		out = append(out, ct)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (e *Engine) Groups() ([]Group, error) {
	if err := e.ready(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	gs, err := e.cli.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(gs))
	for _, g := range gs {
		grp := Group{JID: g.JID.String(), Name: g.Name, Topic: g.Topic}
		e.st.setName(grp.JID, g.Name, 3)
		for _, p := range g.Participants {
			pj := p.JID.ToNonAD().String()
			grp.Members = append(grp.Members, Member{JID: pj, Name: e.st.name(pj), Admin: p.IsAdmin})
		}
		out = append(out, grp)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Resolve turns an alias, JID, phone number or (fuzzy) name into a JID string.
func (e *Engine) Resolve(q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", errors.New("no chat given")
	}
	for k, v := range loadConfig().Aliases {
		if strings.EqualFold(k, q) {
			q = v
			break
		}
	}
	if strings.Contains(q, "@") {
		j, err := types.ParseJID(q)
		if err != nil {
			return "", err
		}
		return j.ToNonAD().String(), nil
	}
	if isPhoneLike(q) {
		return types.NewJID(digitsOnly(q), types.DefaultUserServer).String(), nil
	}
	type cand struct {
		jid, name string
	}
	var best []cand
	top := 0
	for jid, name := range e.st.allNames() {
		s := fuzzyScore(q, name)
		if s == 0 {
			continue
		}
		if s > top {
			top = s
			best = best[:0]
		}
		if s == top {
			best = append(best, cand{jid, name})
		}
	}
	if len(best) == 0 {
		return "", fmt.Errorf("no chat or contact matches %q", q)
	}
	if len(best) > 1 {
		var withChat []cand
		for _, c := range best {
			if e.st.hasChat(c.jid) {
				withChat = append(withChat, c)
			}
		}
		if len(withChat) == 1 {
			return withChat[0].jid, nil
		}
		var names []string
		for i, c := range best {
			if i == 5 {
				names = append(names, "…")
				break
			}
			names = append(names, fmt.Sprintf("%s (%s)", c.name, userPart(c.jid)))
		}
		return "", fmt.Errorf("%q is ambiguous: %s", q, strings.Join(names, ", "))
	}
	return best[0].jid, nil
}

func (e *Engine) chatJID(s string) (types.JID, error) {
	j, err := e.Resolve(s)
	if err != nil {
		return types.EmptyJID, err
	}
	return types.ParseJID(j)
}

func (e *Engine) replyCtx(chat types.JID, id string) (*waE2E.ContextInfo, string, error) {
	qm, _, err := e.st.getMessage(chat.String(), id)
	if err != nil {
		return nil, "", err
	}
	ci := &waE2E.ContextInfo{
		StanzaID:      proto.String(qm.ID),
		Participant:   proto.String(qm.Sender),
		QuotedMessage: &waE2E.Message{Conversation: proto.String(qm.Text)},
	}
	return ci, qm.ID, nil
}

func (e *Engine) sendAndStore(jid types.JID, msg *waE2E.Message, text, kind, fname, replyID string) (SendResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resp, err := e.cli.SendMessage(ctx, jid, msg)
	if err != nil {
		return SendResult{}, err
	}
	m := &Message{
		Chat:     jid.String(),
		ID:       string(resp.ID),
		Sender:   e.meJID(),
		FromMe:   true,
		TS:       resp.Timestamp.Unix(),
		Text:     text,
		Kind:     kind,
		Filename: fname,
		ReplyTo:  replyID,
	}
	_ = e.st.insertMessage(m, nil)
	_ = e.st.touchChat(m.Chat, jid.Server == types.GroupServer, m.TS, previewOf(m), 0)
	e.emit(Event{Type: "message", Chat: m.Chat, Msg: m})
	return SendResult{ID: m.ID, Chat: m.Chat}, nil
}

func (e *Engine) Send(a SendArgs) (SendResult, error) {
	if err := e.ready(); err != nil {
		return SendResult{}, err
	}
	if strings.TrimSpace(a.Text) == "" {
		return SendResult{}, errors.New("empty message")
	}
	jid, err := e.chatJID(a.Chat)
	if err != nil {
		return SendResult{}, err
	}
	msg := &waE2E.Message{Conversation: proto.String(a.Text)}
	replyID := ""
	if a.ReplyTo != "" {
		ci, rid, err := e.replyCtx(jid, a.ReplyTo)
		if err != nil {
			return SendResult{}, err
		}
		replyID = rid
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String(a.Text),
			ContextInfo: ci,
		}}
	}
	return e.sendAndStore(jid, msg, a.Text, "text", "", replyID)
}

func (e *Engine) SendFile(a FileArgs) (SendResult, error) {
	if err := e.ready(); err != nil {
		return SendResult{}, err
	}
	jid, err := e.chatJID(a.Chat)
	if err != nil {
		return SendResult{}, err
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return SendResult{}, err
	}
	if len(data) == 0 {
		return SendResult{}, errors.New("file is empty")
	}
	mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(a.Path)))
	if mt == "" {
		mt = http.DetectContentType(data)
	}
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	var mtype whatsmeow.MediaType
	kind := "document"
	switch {
	case strings.HasPrefix(mt, "image/"):
		mtype, kind = whatsmeow.MediaImage, "image"
	case strings.HasPrefix(mt, "video/"):
		mtype, kind = whatsmeow.MediaVideo, "video"
	case strings.HasPrefix(mt, "audio/"):
		mtype, kind = whatsmeow.MediaAudio, "audio"
	default:
		mtype = whatsmeow.MediaDocument
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	up, err := e.cli.Upload(ctx, data, mtype)
	if err != nil {
		return SendResult{}, err
	}
	var ci *waE2E.ContextInfo
	replyID := ""
	if a.ReplyTo != "" {
		ci, replyID, err = e.replyCtx(jid, a.ReplyTo)
		if err != nil {
			return SendResult{}, err
		}
	}
	name := filepath.Base(a.Path)
	msg := &waE2E.Message{}
	switch kind {
	case "image":
		msg.ImageMessage = &waE2E.ImageMessage{
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			Mimetype: &mt, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: &up.FileLength, Caption: proto.String(a.Caption), ContextInfo: ci,
		}
	case "video":
		msg.VideoMessage = &waE2E.VideoMessage{
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			Mimetype: &mt, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: &up.FileLength, Caption: proto.String(a.Caption), ContextInfo: ci,
		}
	case "audio":
		msg.AudioMessage = &waE2E.AudioMessage{
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			Mimetype: &mt, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: &up.FileLength, ContextInfo: ci,
		}
	default:
		msg.DocumentMessage = &waE2E.DocumentMessage{
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			Mimetype: &mt, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: &up.FileLength, FileName: proto.String(name), Title: proto.String(name),
			Caption: proto.String(a.Caption), ContextInfo: ci,
		}
	}
	fname := ""
	if kind == "document" {
		fname = name
	}
	return e.sendAndStore(jid, msg, a.Caption, kind, fname, replyID)
}

func (e *Engine) Download(a DlArgs) (string, error) {
	if err := e.ready(); err != nil {
		return "", err
	}
	jid, err := e.Resolve(a.Chat)
	if err != nil {
		return "", err
	}
	m, raw, err := e.st.getMessage(jid, a.ID)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("this message has no downloadable media (only media received while wacli was running can be downloaded)")
	}
	var pm waE2E.Message
	if err := proto.Unmarshal(raw, &pm); err != nil {
		return "", err
	}
	var dl whatsmeow.DownloadableMessage
	ext := ""
	switch {
	case pm.GetImageMessage() != nil:
		dl, ext = pm.GetImageMessage(), ".jpg"
	case pm.GetVideoMessage() != nil:
		dl, ext = pm.GetVideoMessage(), ".mp4"
	case pm.GetAudioMessage() != nil:
		dl, ext = pm.GetAudioMessage(), ".ogg"
	case pm.GetDocumentMessage() != nil:
		dl = pm.GetDocumentMessage()
	case pm.GetStickerMessage() != nil:
		dl, ext = pm.GetStickerMessage(), ".webp"
	default:
		return "", errors.New("no media in this message")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	data, err := e.cli.Download(ctx, dl)
	if err != nil {
		return "", err
	}
	dir := a.Dir
	if dir == "" {
		dir = loadConfig().DownloadDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(m.Filename)
	if m.Filename == "" || name == "." || name == "/" {
		name = short(m.ID) + ext
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		path = filepath.Join(dir, short(m.ID)+"-"+name)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (e *Engine) React(a ReactArgs) error {
	if err := e.ready(); err != nil {
		return err
	}
	jid, err := e.chatJID(a.Chat)
	if err != nil {
		return err
	}
	m, _, err := e.st.getMessage(jid.String(), a.ID)
	if err != nil {
		return err
	}
	sj, err := types.ParseJID(m.Sender)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	msg := e.cli.BuildReaction(jid, sj, types.MessageID(m.ID), a.Emoji)
	if _, err := e.cli.SendMessage(ctx, jid, msg); err != nil {
		return err
	}
	e.st.setReaction(jid.String(), m.ID, e.meJID(), a.Emoji)
	e.emit(Event{Type: "update", Chat: jid.String()})
	return nil
}

func (e *Engine) Edit(a EditArgs) error {
	if err := e.ready(); err != nil {
		return err
	}
	jid, err := e.chatJID(a.Chat)
	if err != nil {
		return err
	}
	m, _, err := e.st.getMessage(jid.String(), a.ID)
	if err != nil {
		return err
	}
	if !m.FromMe {
		return errors.New("you can only edit your own messages")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	msg := e.cli.BuildEdit(jid, types.MessageID(m.ID), &waE2E.Message{Conversation: proto.String(a.Text)})
	if _, err := e.cli.SendMessage(ctx, jid, msg); err != nil {
		return err
	}
	e.st.markEdited(jid.String(), m.ID, a.Text)
	e.emit(Event{Type: "update", Chat: jid.String()})
	return nil
}

func (e *Engine) Delete(a MsgRef) error {
	if err := e.ready(); err != nil {
		return err
	}
	jid, err := e.chatJID(a.Chat)
	if err != nil {
		return err
	}
	m, _, err := e.st.getMessage(jid.String(), a.ID)
	if err != nil {
		return err
	}
	if !m.FromMe {
		return errors.New("you can only delete your own messages")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	msg := e.cli.BuildRevoke(jid, types.EmptyJID, types.MessageID(m.ID))
	if _, err := e.cli.SendMessage(ctx, jid, msg); err != nil {
		return err
	}
	e.st.markDeleted(jid.String(), m.ID)
	e.emit(Event{Type: "update", Chat: jid.String()})
	return nil
}

// MarkRead sends read receipts (blue ticks) for everything unread in a chat.
func (e *Engine) MarkRead(chat string) error {
	jid, err := e.chatJID(chat)
	if err != nil {
		return err
	}
	pend, err := e.st.unreadIncoming(jid.String())
	if err != nil {
		return err
	}
	if len(pend) > 0 && loadConfig().SendReceipts && e.connected.Load() {
		bySender := map[string][]types.MessageID{}
		for _, p := range pend {
			bySender[p.Sender] = append(bySender[p.Sender], types.MessageID(p.ID))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for sender, ids := range bySender {
			sj, err := types.ParseJID(sender)
			if err != nil {
				continue
			}
			_ = e.cli.MarkRead(ctx, ids, time.Now(), jid, sj)
		}
	}
	e.st.clearUnread(jid.String())
	return nil
}

func (e *Engine) Typing(a TypingArgs) error {
	if !loadConfig().SendTyping || !e.connected.Load() {
		return nil
	}
	jid, err := e.chatJID(a.Chat)
	if err != nil {
		return err
	}
	state := types.ChatPresencePaused
	if a.On {
		state = types.ChatPresenceComposing
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return e.cli.SendChatPresence(ctx, jid, state, types.ChatPresenceMediaText)
}

// Presence returns the last known online/last-seen state and (re)subscribes to updates.
func (e *Engine) Presence(chat string) (PresenceInfo, error) {
	jid, err := e.chatJID(chat)
	if err != nil {
		return PresenceInfo{}, err
	}
	if jid.Server == types.DefaultUserServer && e.connected.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.cli.SubscribePresence(ctx, jid)
	}
	e.presMu.Lock()
	defer e.presMu.Unlock()
	return e.pres[jid.ToNonAD().String()], nil
}

func (e *Engine) SetOnline(on bool) error {
	if !e.connected.Load() {
		return nil
	}
	p := types.PresenceUnavailable
	if on {
		p = types.PresenceAvailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return e.cli.SendPresence(ctx, p)
}

func (e *Engine) SetActive(chat string) error {
	if chat != "" {
		if j, err := e.Resolve(chat); err == nil {
			chat = j
		}
	}
	e.active.Store(chat)
	return nil
}
