package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

// ================================================================ themes

type theme struct {
	bg, bar, me, them, accent, sel, pill, text, dim, tick lipgloss.Color
}

var themes = map[string]theme{
	// WhatsApp dark
	"whatsapp": {bg: "#0B141A", bar: "#202C33", me: "#005C4B", them: "#202C33", accent: "#00A884",
		sel: "#2A3942", pill: "#182229", text: "#E9EDEF", dim: "#8696A0", tick: "#53BDEB"},
	// WhatsApp light
	"light": {bg: "#EFEAE2", bar: "#F0F2F5", me: "#D9FDD3", them: "#FFFFFF", accent: "#00A884",
		sel: "#D9DBDF", pill: "#FFFFFF", text: "#111B21", dim: "#667781", tick: "#53BDEB"},
	"ocean": {bg: "#0B1620", bar: "#16283A", me: "#14507A", them: "#1B2B3A", accent: "#3BA3E0",
		sel: "#223A52", pill: "#13222F", text: "#E6EEF5", dim: "#8AA0B2", tick: "#7CD0FF"},
	"purple": {bg: "#130F1C", bar: "#241B35", me: "#5B2E91", them: "#2A2140", accent: "#A97BFF",
		sel: "#332651", pill: "#1B1429", text: "#EEE9F7", dim: "#9A8FB0", tick: "#C3A6FF"},
	"mono": {bg: "#111111", bar: "#1E1E1E", me: "#3A3A3A", them: "#242424", accent: "#D0D0D0",
		sel: "#2E2E2E", pill: "#1A1A1A", text: "#EEEEEE", dim: "#8A8A8A", tick: "#FFFFFF"},
}

var th = themes["whatsapp"]

const warnColor = lipgloss.Color("#FFB000")
const draftColor = lipgloss.Color("#F15C6D")

// Keys arriving closer together than this are a paste, not typing: an Enter inside
// a paste becomes a new line instead of sending the message.
const pasteGap = 40 * time.Millisecond

func themeNames() []string {
	names := make([]string, 0, len(themes))
	for k := range themes {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func applyTheme(name string) bool {
	t, ok := themes[strings.ToLower(strings.TrimSpace(name))]
	if ok {
		th = t
	}
	return ok
}

var senderColors = []lipgloss.Color{"#53BDEB", "#FFA97A", "#E26AB6", "#06CF9C", "#FFD279", "#A5B337", "#B292EB", "#FF7B6B", "#5ABB66", "#7FA4FF"}
var avatarBgs = []lipgloss.Color{"#25786F", "#7E5B9A", "#9A6B3C", "#3C6E9A", "#8A4E5E", "#5B7F3A", "#8C7A2E", "#4E5BA3"}

func hashIdx(s string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum32() % uint32(n))
}

// ================================================================ drawing helpers

type seg struct {
	t    string
	fg   lipgloss.Color
	bg   lipgloss.Color
	bold bool
	ital bool
}

func segStyle(s seg, bg lipgloss.Color) lipgloss.Style {
	st := lipgloss.NewStyle()
	b := bg
	if s.bg != "" {
		b = s.bg
	}
	if b != "" {
		st = st.Background(b)
	}
	if s.fg != "" {
		st = st.Foreground(s.fg)
	}
	if s.bold {
		st = st.Bold(true)
	}
	if s.ital {
		st = st.Italic(true)
	}
	return st
}

// render paints segments on a background and pads the row to exactly `width` columns.
func render(bg lipgloss.Color, width int, segs ...seg) string {
	var b strings.Builder
	used := 0
	for _, s := range segs {
		if s.t == "" {
			continue
		}
		b.WriteString(segStyle(s, bg).Render(s.t))
		used += runewidth.StringWidth(s.t)
	}
	if used < width {
		b.WriteString(segStyle(seg{}, bg).Render(strings.Repeat(" ", width-used)))
	}
	return b.String()
}

func bgr(w int, segs ...seg) string { return render(th.bg, w, segs...) }
func blankRow(w int) string         { return render(th.bg, w) }

func segsWidth(segs []seg) int {
	n := 0
	for _, s := range segs {
		n += runewidth.StringWidth(s.t)
	}
	return n
}

func clamp(v, lo, hi int) int {
	if hi < lo || v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func spaces(n int) string {
	if n < 0 {
		n = 0
	}
	return strings.Repeat(" ", n)
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.Truncate(s, w, "…")
}

func clean(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\t", "  ")
}

func wrapText(s string, w int) []string {
	if w < 4 {
		w = 4
	}
	out := lipgloss.NewStyle().Width(w).Render(s)
	lines := strings.Split(out, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}

func initials(name string) string {
	f := strings.Fields(name)
	if len(f) == 0 {
		return "?"
	}
	out := string(unicode.ToUpper([]rune(f[0])[0]))
	if len(f) > 1 {
		out += string(unicode.ToUpper([]rune(f[1])[0]))
	}
	return out
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func listTime(ts int64) string {
	if ts == 0 {
		return ""
	}
	t := time.Unix(ts, 0)
	d0 := dayStart(time.Now())
	switch {
	case !t.Before(d0):
		return t.Format("15:04")
	case !t.Before(d0.AddDate(0, 0, -1)):
		return "Yesterday"
	case !t.Before(d0.AddDate(0, 0, -6)):
		return t.Format("Mon")
	}
	return t.Format("02/01/06")
}

func dayLabel(ts int64) string {
	t := time.Unix(ts, 0)
	d0 := dayStart(time.Now())
	switch {
	case !t.Before(d0):
		return "Today"
	case !t.Before(d0.AddDate(0, 0, -1)):
		return "Yesterday"
	}
	return t.Format("Mon, 02 Jan 2006")
}

func kindLabel(mm Message) string {
	switch mm.Kind {
	case "image":
		return "📷 Photo"
	case "video":
		return "🎥 Video"
	case "audio":
		return "🎵 Audio"
	case "sticker":
		return "🙂 Sticker"
	case "location":
		return "📍 Location"
	case "document":
		if mm.Filename != "" {
			return "📄 " + mm.Filename
		}
		return "📄 Document"
	}
	return ""
}

func prettyPreview(s string) string {
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			rest := strings.TrimSpace(s[i+1:])
			icon := ""
			switch s[1:i] {
			case "image":
				icon = "📷 Photo"
			case "video":
				icon = "🎥 Video"
			case "audio":
				icon = "🎵 Audio"
			case "sticker":
				icon = "🙂 Sticker"
			case "location":
				icon = "📍 Location"
			case "document":
				icon = "📄 Document"
			}
			if icon != "" {
				return strings.TrimSpace(icon + " " + rest)
			}
		}
	}
	return s
}

// chatKind: 0 = personal chat, 1 = group, 2 = channel, -1 = hidden (status/broadcast).
func chatKind(jid string) int {
	switch {
	case strings.HasSuffix(jid, "@g.us"):
		return 1
	case strings.HasSuffix(jid, "@newsletter"):
		return 2
	case strings.HasSuffix(jid, "@broadcast"):
		return -1
	}
	return 0
}

func tabName(k int) string  { return [...]string{"Chats", "Groups", "Channels", "Unread"}[k] }
func tabShort(k int) string { return [...]string{"Chats", "Grp", "Chan", "Unread"}[k] }

func unreadLabel(n int) string {
	if n > 99 {
		return "99+"
	}
	return strconv.Itoa(n)
}

// hintRow draws "key label" pairs, dropping the ones that don't fit.
func hintRow(w int, pairs ...string) string {
	segs := []seg{{t: " "}}
	used := 1
	for i := 0; i+1 < len(pairs); i += 2 {
		k := seg{t: " " + pairs[i] + " ", fg: th.text, bg: th.sel, bold: true}
		l := seg{t: " " + pairs[i+1] + "  ", fg: th.dim}
		need := segsWidth([]seg{k, l})
		if used+need > w {
			break
		}
		segs = append(segs, k, l)
		used += need
	}
	return bgr(w, segs...)
}

// ================================================================ model

type chatsMsg []Chat
type statusMsg Status
type msgsMsg struct {
	chat string
	msgs []Message
	info string
}
type presMsg struct {
	chat string
	p    PresenceInfo
}
type contactsMsg []Contact
type searchMsg struct {
	q    string
	hits []SearchHit
}

// searchRes is one row of the global search results (chat, contact or message).
type searchRes struct {
	kind  int // 0 chat, 1 contact (no history), 2 message
	jid   string
	title string
	sub   string
	ts    int64
	msgID string
}
type errMsg struct{ err error }
type evMsg Event
type evClosedMsg struct{}
type tickMsg time.Time

type tuiModel struct {
	be     Backend
	w, h   int
	online bool
	st     Status // account info for the dashboard

	chats     []Chat
	shown     []Chat
	tab       int
	sel       int
	selJID    string
	filter    textinput.Model
	filtering bool

	cur     string
	curName string
	msgs    []Message
	scroll  int
	inChat  bool
	input   textinput.Model
	pres    PresenceInfo

	typing      bool
	typingUntil time.Time
	lastTyping  time.Time

	selMsg     int // -1 = typing mode, >=0 = picking a message
	reply      Message
	hasReply   bool
	editID     string
	picker     bool
	confirmDel bool
	showHelp   bool

	// contacts picker (WhatsApp-style "new chat")
	contactsMode bool
	contacts     []Contact
	cshown       []Contact
	csel         int
	cfilter      textinput.Model

	// home dashboard
	showDash bool
	dashHits []dashHit

	// drafts (kept per chat, saved to ~/.wacli/drafts.json)
	drafts  map[string]string
	lines   []string // finished lines of a multi-line paste; sent together as ONE message
	lastKey time.Time
	tickN   int

	// global search (the / box)
	res        []searchRes
	rsel       int
	hits       []SearchHit
	jumpID     string
	contactsOK bool

	typingIn map[string]time.Time // chats where someone is typing right now (list preview)
	pinned   map[string]bool      // pinned chats (★), saved in config.json

	// file browser (the 📁 button): browse the download folder, open or send a file
	showFiles bool
	fStage    int // 0 file list · 1 open/send · 2 pick a chat · 3 confirm
	fDir      string
	fItems    []fileItem
	fSel      int
	fFile     fileItem
	fchats    []Chat
	fcSel     int
	ffilter   textinput.Model
	fTarget   Chat

	// swipe on the chat list: the press is remembered and decided on release
	// (a short move = tap, a wide horizontal move = switch tab)
	pressing       bool
	pressX, pressY int

	status string
	events <-chan Event
	cancel func()
}

func (m *tuiModel) restyle() {
	st := lipgloss.NewStyle().Background(th.bg)
	m.input.PromptStyle = st.Foreground(th.accent).Bold(true)
	m.input.TextStyle = st.Foreground(th.text)
	m.input.PlaceholderStyle = st.Foreground(th.dim)
	m.filter.PromptStyle = m.input.PromptStyle
	m.filter.TextStyle = m.input.TextStyle
	m.filter.PlaceholderStyle = m.input.PlaceholderStyle
	m.cfilter.PromptStyle = m.input.PromptStyle
	m.cfilter.TextStyle = m.input.TextStyle
	m.cfilter.PlaceholderStyle = m.input.PlaceholderStyle
	m.ffilter.PromptStyle = m.input.PromptStyle
	m.ffilter.TextStyle = m.input.TextStyle
	m.ffilter.PlaceholderStyle = m.input.PlaceholderStyle
}

func runTUI(b Backend) error {
	if os.Getenv("WACLI_256") == "" {
		lipgloss.SetColorProfile(termenv.TrueColor) // Termux supports 24-bit colour
	}
	applyTheme(loadConfig().Theme)
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = "Type a message…  (/help)"
	ti.CharLimit = 60000
	fi := textinput.New()
	fi.Prompt = "🔍 "
	fi.Placeholder = "search"

	ci := textinput.New()
	ci.Prompt = "🔍 "
	ci.Placeholder = "name or number"

	ffi := textinput.New()
	ffi.Prompt = "🔍 "
	ffi.Placeholder = "send to… (name)"

	m := &tuiModel{be: b, input: ti, filter: fi, cfilter: ci, ffilter: ffi, selMsg: -1,
		typingIn: map[string]time.Time{}, pinned: map[string]bool{}, drafts: loadDrafts()}
	for _, j := range loadConfig().Pinned {
		m.pinned[j] = true
	}
	m.restyle()
	if ch, cancel, err := b.Subscribe(); err == nil {
		m.events, m.cancel = ch, cancel
	} else {
		m.status = "⚠ live updates unavailable: " + err.Error()
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// ---------------------------------------------------------------- async commands

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *tuiModel) waitEvent() tea.Cmd {
	ch := m.events
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return evClosedMsg{}
		}
		return evMsg(ev)
	}
}

func (m *tuiModel) loadChats() tea.Cmd {
	be := m.be
	return func() tea.Msg {
		cs, err := be.Chats()
		if err != nil {
			return errMsg{err}
		}
		return chatsMsg(cs)
	}
}

func (m *tuiModel) loadContacts() tea.Cmd {
	be := m.be
	return func() tea.Msg {
		cs, err := be.Contacts()
		if err != nil {
			return errMsg{err}
		}
		return contactsMsg(cs)
	}
}

func (m *tuiModel) loadStatus() tea.Cmd {
	be := m.be
	return func() tea.Msg {
		s, err := be.Status()
		if err != nil {
			return nil
		}
		return statusMsg(s)
	}
}

func (m *tuiModel) loadMsgs(chat string) tea.Cmd {
	be := m.be
	return func() tea.Msg {
		ms, err := be.Messages(MsgQuery{Chat: chat, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return msgsMsg{chat: chat, msgs: ms}
	}
}

func (m *tuiModel) loadPresence(chat string) tea.Cmd {
	be := m.be
	return func() tea.Msg {
		p, err := be.Presence(chat)
		if err != nil {
			return nil
		}
		return presMsg{chat: chat, p: p}
	}
}

func (m *tuiModel) markRead(chat string) tea.Cmd {
	be := m.be
	return func() tea.Msg {
		_ = be.MarkRead(chat)
		cs, err := be.Chats()
		if err != nil {
			return errMsg{err}
		}
		return chatsMsg(cs)
	}
}

func (m *tuiModel) typingCmd(chat string, on bool) tea.Cmd {
	be := m.be
	return func() tea.Msg {
		_ = be.Typing(TypingArgs{Chat: chat, On: on})
		return nil
	}
}

// act runs f, then reloads the open chat and shows f's result text in the status line.
func (m *tuiModel) act(f func() (string, error)) tea.Cmd {
	be := m.be
	chat := m.cur
	return func() tea.Msg {
		info, err := f()
		if err != nil {
			return errMsg{err}
		}
		ms, err := be.Messages(MsgQuery{Chat: chat, Limit: 200})
		if err != nil {
			return errMsg{err}
		}
		return msgsMsg{chat: chat, msgs: ms, info: info}
	}
}

func (m *tuiModel) Init() tea.Cmd {
	be := m.be
	return tea.Batch(
		m.loadChats(),
		m.loadStatus(),
		m.waitEvent(),
		tickCmd(),
		func() tea.Msg { _ = be.SetOnline(true); return nil },
	)
}

// ---------------------------------------------------------------- update

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.Width = max(8, m.paneWidths()[1]-4)
		m.filter.Width = max(8, m.paneWidths()[0]-6)
		m.cfilter.Width = max(8, m.paneWidths()[0]-6)
		m.ffilter.Width = max(8, msg.Width-6)
	case contactsMsg:
		var out []Contact
		for _, c := range []Contact(msg) {
			if chatKind(c.JID) != 0 || (strings.TrimSpace(c.Name) == "" && c.Phone == "") {
				continue
			}
			out = append(out, c)
		}
		m.contacts = out
		m.contactsOK = true
		m.refilterContacts()
		if m.searching() {
			m.rebuildResults()
		}
	case searchMsg:
		if msg.q == strings.TrimSpace(m.filter.Value()) {
			m.hits = msg.hits
			m.rebuildResults()
		}
	case chatsMsg:
		m.chats = []Chat(msg)
		m.refilter()
		if m.searching() {
			m.rebuildResults()
		}
	case statusMsg:
		m.st = Status(msg)
		m.online = msg.Connected
	case msgsMsg:
		if msg.chat == m.cur {
			m.msgs = msg.msgs
			if m.jumpID != "" { // opened from a message search hit: jump to that message
				for i, mm := range m.msgs {
					if mm.ID == m.jumpID {
						m.selMsg = i
						m.input.Blur()
						break
					}
				}
				m.jumpID = ""
			}
			if m.selMsg >= len(m.msgs) {
				m.selMsg = len(m.msgs) - 1
			}
		}
		if msg.info != "" {
			m.status = msg.info
		}
	case presMsg:
		if msg.chat == m.cur {
			m.pres = msg.p
		}
	case errMsg:
		m.status = "⚠ " + msg.err.Error()
	case tickMsg:
		now := time.Now()
		if m.typing && now.After(m.typingUntil) {
			m.typing = false
		}
		for j, until := range m.typingIn {
			if now.After(until) {
				delete(m.typingIn, j)
			}
		}
		m.tickN++
		if m.inChat && m.tickN%3 == 0 {
			m.syncDraft() // keep the draft on disk while typing
		}
		return m, tickCmd()
	case evClosedMsg:
		m.status = "⚠ lost connection to the daemon"
	case evMsg:
		return m.onEvent(Event(msg))
	case tea.KeyMsg:
		return m.onKey(msg)
	case tea.MouseMsg:
		return m.onMouse(msg)
	}
	return m, nil
}

func (m *tuiModel) onEvent(ev Event) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{m.waitEvent()}
	switch ev.Type {
	case "message", "update":
		if ev.Type == "message" && ev.Msg != nil && !ev.Msg.FromMe {
			delete(m.typingIn, ev.Chat) // they sent it, so they stopped typing
		}
		cmds = append(cmds, m.loadChats())
		if ev.Chat != "" && ev.Chat == m.cur {
			cmds = append(cmds, m.loadMsgs(m.cur))
			if ev.Type == "message" && ev.Msg != nil && !ev.Msg.FromMe && m.inChat {
				cmds = append(cmds, m.markRead(m.cur))
			}
		}
	case "presence":
		if ev.Chat == m.cur {
			cmds = append(cmds, m.loadPresence(m.cur))
		}
	case "typing":
		if ev.Chat != "" {
			if ev.On {
				m.typingIn[ev.Chat] = time.Now().Add(7 * time.Second)
			} else {
				delete(m.typingIn, ev.Chat)
			}
		}
		if ev.Chat == m.cur {
			m.typing = ev.On
			m.typingUntil = time.Now().Add(7 * time.Second)
		}
	case "connected":
		m.online = true
		cmds = append(cmds, m.loadStatus())
	case "disconnected":
		m.online = false
	}
	return m, tea.Batch(cmds...)
}

// ---- tabs (Chats / Groups / Channels)

func (m *tuiModel) tabList() []int {
	list := []int{0, 1}
	for _, c := range m.chats {
		if chatKind(c.JID) == 2 {
			list = append(list, 2)
			break
		}
	}
	return append(list, 3) // 3 = Unread
}

func (m *tuiModel) tabUnread(kind int) int {
	n := 0
	for _, c := range m.chats {
		k := chatKind(c.JID)
		if k < 0 {
			continue
		}
		if kind == 3 || k == kind {
			n += c.Unread
		}
	}
	return n
}

// unreadChats: every visible chat with unread messages, most unread first.
func (m *tuiModel) unreadChats() []Chat {
	var out []Chat
	for _, c := range m.chats {
		if chatKind(c.JID) >= 0 && c.Unread > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Unread != out[j].Unread {
			return out[i].Unread > out[j].Unread
		}
		return out[i].LastTS > out[j].LastTS
	})
	return out
}

func (m *tuiModel) setTab(k int) {
	ok := false
	for _, t := range m.tabList() {
		if t == k {
			ok = true
		}
	}
	if !ok || k == m.tab {
		return
	}
	m.tab = k
	m.sel = 0
	m.selJID = ""
	m.refilter()
}

func (m *tuiModel) switchTab(d int) {
	list := m.tabList()
	idx := 0
	for i, k := range list {
		if k == m.tab {
			idx = i
		}
	}
	m.setTab(list[clamp(idx+d, 0, len(list)-1)])
}

func (m *tuiModel) refilter() {
	q := strings.TrimSpace(m.filter.Value())
	var out []Chat
	for _, c := range m.chats {
		k := chatKind(c.JID)
		if m.tab == 3 {
			if k < 0 || c.Unread == 0 {
				continue
			}
		} else if k != m.tab {
			continue
		}
		if m.filtering && q != "" && fuzzyScore(q, c.Title()) == 0 {
			continue
		}
		out = append(out, c)
	}
	if len(m.pinned) > 0 { // pinned chats go to the top, order otherwise unchanged
		var top, rest []Chat
		for _, c := range out {
			if m.pinned[c.JID] {
				top = append(top, c)
			} else {
				rest = append(rest, c)
			}
		}
		out = append(top, rest...)
	}
	m.shown = out
	m.fixSel()
}

// togglePin pins / unpins a chat and saves the list to config.json.
func (m *tuiModel) togglePin(jid string) bool {
	if jid == "" {
		return false
	}
	if m.pinned[jid] {
		delete(m.pinned, jid)
	} else {
		m.pinned[jid] = true
	}
	cfg := loadConfig()
	var list []string
	seen := map[string]bool{}
	for _, j := range cfg.Pinned { // keep the saved order for chats that stay pinned
		if m.pinned[j] && !seen[j] {
			list = append(list, j)
			seen[j] = true
		}
	}
	if m.pinned[jid] && !seen[jid] {
		list = append(list, jid)
	}
	cfg.Pinned = list
	_ = cfg.Save()
	m.refilter()
	return m.pinned[jid]
}

func (m *tuiModel) fixSel() {
	if len(m.shown) == 0 {
		m.sel = 0
		return
	}
	if m.selJID != "" {
		for i, c := range m.shown {
			if c.JID == m.selJID {
				m.sel = i
				return
			}
		}
	}
	m.sel = clamp(m.sel, 0, len(m.shown)-1)
	m.selJID = m.shown[m.sel].JID
}

func (m *tuiModel) moveSel(d int) {
	if len(m.shown) == 0 {
		return
	}
	m.sel = clamp(m.sel+d, 0, len(m.shown)-1)
	m.selJID = m.shown[m.sel].JID
}

func (m *tuiModel) quit() tea.Cmd {
	m.syncDraft()
	if m.cancel != nil {
		m.cancel()
	}
	be := m.be
	chat := m.cur
	return func() tea.Msg {
		if chat != "" {
			_ = be.Typing(TypingArgs{Chat: chat, On: false})
		}
		_ = be.SetActive("")
		_ = be.SetOnline(false)
		return tea.Quit()
	}
}

func (m *tuiModel) resetChatState() {
	m.selMsg = -1
	m.hasReply = false
	m.editID = ""
	m.picker = false
	m.confirmDel = false
}

func (m *tuiModel) openSel() tea.Cmd {
	if len(m.shown) == 0 {
		return nil
	}
	c := m.shown[m.sel]
	return m.openJID(c.JID, c.Title())
}

// openJID opens any chat, including a contact that has no history yet.
func (m *tuiModel) openJID(jid, name string) tea.Cmd {
	var prev tea.Cmd
	m.syncDraft() // save what was typed in the chat we are leaving
	if m.cur != "" && m.cur != jid {
		prev = m.typingCmd(m.cur, false)
	}
	m.res, m.rsel = nil, 0
	m.contactsMode = false
	m.cfilter.Blur()
	m.showDash = false
	m.cur, m.curName = jid, name
	m.msgs, m.scroll, m.pres, m.typing = nil, 0, PresenceInfo{}, false
	m.resetChatState()
	m.filtering = false
	m.filter.SetValue("")
	m.filter.Blur()
	m.refilter()
	m.input.SetValue("")
	m.lines = nil
	if d := m.drafts[jid]; d != "" {
		m.setDraft(d) // bring back the unsent text
	}
	m.inChat = true
	m.input.Focus()
	be := m.be
	return tea.Batch(
		prev,
		textinput.Blink,
		m.loadMsgs(jid),
		m.markRead(jid),
		m.loadPresence(jid),
		func() tea.Msg { _ = be.SetActive(jid); return nil },
	)
}

func (m *tuiModel) leaveChat() tea.Cmd {
	m.syncDraft()
	chat := m.cur
	m.inChat = false
	m.input.Blur()
	m.resetChatState()
	be := m.be
	return func() tea.Msg {
		_ = be.Typing(TypingArgs{Chat: chat, On: false})
		_ = be.SetActive("")
		return nil
	}
}

func (m *tuiModel) selected() (Message, bool) {
	if m.selMsg < 0 || m.selMsg >= len(m.msgs) {
		return Message{}, false
	}
	return m.msgs[m.selMsg], true
}

func (m *tuiModel) exitSelect() tea.Cmd {
	m.selMsg = -1
	m.picker = false
	m.input.Focus()
	return textinput.Blink
}

func (m *tuiModel) doDelete(mm Message) tea.Cmd {
	be, chat, id := m.be, m.cur, mm.ID
	return m.act(func() (string, error) {
		return "message deleted", be.Delete(MsgRef{Chat: chat, ID: id})
	})
}

func (m *tuiModel) doReact(mm Message, emoji string) tea.Cmd {
	be, chat, id := m.be, m.cur, mm.ID
	return m.act(func() (string, error) {
		return "reacted " + emoji, be.React(ReactArgs{Chat: chat, ID: id, Emoji: emoji})
	})
}

func (m *tuiModel) doDownload(mm Message) tea.Cmd {
	be, chat, id := m.be, m.cur, mm.ID
	return m.act(func() (string, error) {
		p, err := be.Download(DlArgs{Chat: chat, ID: id})
		if err != nil {
			return "", err
		}
		return "saved: " + p, nil
	})
}

func (m *tuiModel) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, m.quit()
	}
	m.status = ""
	if m.showFiles {
		return m.onFilesKey(k, s)
	}
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	if m.showDash {
		m.showDash = false
		return m, nil
	}
	if !m.inChat {
		return m.onListKey(k, s)
	}
	return m.onChatKey(k, s)
}

func (m *tuiModel) onListKey(k tea.KeyMsg, s string) (tea.Model, tea.Cmd) {
	if m.contactsMode {
		return m.onContactsKey(k, s)
	}
	if m.filtering {
		switch s {
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.res, m.rsel = nil, 0
			m.refilter()
			return m, nil
		case "enter":
			if m.searching() {
				return m, m.openRes()
			}
			return m, m.openSel()
		case "up":
			if m.searching() {
				m.moveRes(-1)
			} else {
				m.moveSel(-1)
			}
			return m, nil
		case "down":
			if m.searching() {
				m.moveRes(1)
			} else {
				m.moveSel(1)
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(k)
		m.refilter()
		return m, tea.Batch(cmd, m.runSearch())
	}
	switch s {
	case "q":
		return m, m.quit()
	case "?":
		m.showHelp = true
	case "/":
		m.filtering = true
		m.filter.Focus()
		if !m.contactsOK {
			return m, tea.Batch(textinput.Blink, m.loadContacts()) // contacts are searched too
		}
		return m, textinput.Blink
	case "up", "k":
		m.moveSel(-1)
	case "down", "j":
		m.moveSel(1)
	case "g":
		m.moveSel(-len(m.shown))
	case "G":
		m.moveSel(len(m.shown))
	case "left", "h":
		m.switchTab(-1)
	case "right", "l":
		m.switchTab(1)
	case "1":
		m.setTab(0)
	case "2":
		m.setTab(1)
	case "3":
		m.setTab(2)
	case "4":
		m.setTab(3)
	case "u":
		if m.tab == 3 {
			m.setTab(0)
		} else {
			m.setTab(3)
		}
	case "p":
		if len(m.shown) > 0 {
			m.selJID = m.shown[m.sel].JID // keep the cursor on this chat after it moves
			m.togglePin(m.selJID)
		}
	case "c", "n":
		return m, m.openContacts()
	case "d":
		m.showDash = true
	case "f":
		return m, m.openFiles()
	case "t":
		m.cycleTheme()
	case "enter":
		return m, m.openSel()
	case "tab":
		if m.cur != "" {
			m.inChat = true
			m.input.Focus()
			return m, textinput.Blink
		}
		return m, m.openSel()
	}
	return m, nil
}

func (m *tuiModel) onChatKey(k tea.KeyMsg, s string) (tea.Model, tea.Cmd) {
	page := max(3, m.h-6)

	if m.confirmDel {
		m.confirmDel = false
		if s == "y" || s == "Y" {
			if mm, ok := m.selected(); ok {
				cmd := m.doDelete(mm)
				return m, tea.Batch(m.exitSelect(), cmd)
			}
		}
		return m, nil
	}

	if m.picker {
		emojis := []string{"👍", "❤️", "😂", "😮", "😢", "🙏"}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(emojis) {
			if mm, ok := m.selected(); ok {
				cmd := m.doReact(mm, emojis[n-1])
				return m, tea.Batch(m.exitSelect(), cmd)
			}
		}
		m.picker = false
		return m, nil
	}

	if m.selMsg >= 0 { // picking a message
		switch s {
		case "up", "k":
			m.selMsg = max(0, m.selMsg-1)
		case "down", "j":
			if m.selMsg >= len(m.msgs)-1 {
				return m, m.exitSelect()
			}
			m.selMsg++
		case "pgup", "ctrl+u":
			m.selMsg = max(0, m.selMsg-5)
		case "pgdown", "ctrl+d":
			m.selMsg = min(len(m.msgs)-1, m.selMsg+5)
		case "esc", "i":
			return m, m.exitSelect()
		case "r", "enter":
			if mm, ok := m.selected(); ok {
				m.reply, m.hasReply, m.editID = mm, true, ""
				return m, m.exitSelect()
			}
		case "e":
			if mm, ok := m.selected(); ok && mm.FromMe && mm.Kind == "text" && !mm.Deleted {
				m.editID, m.hasReply = mm.ID, false
				m.lines = nil
				c := m.exitSelect()
				m.input.SetValue(mm.Text)
				return m, c
			}
			m.status = "⚠ you can only edit your own text messages"
		case "d":
			if mm, ok := m.selected(); ok && mm.FromMe && !mm.Deleted {
				m.confirmDel = true
			} else {
				m.status = "⚠ you can only delete your own messages"
			}
		case "g":
			if mm, ok := m.selected(); ok && mm.HasMedia {
				return m, m.doDownload(mm)
			}
			m.status = "⚠ no downloadable media in this message"
		case "+", "x":
			m.picker = true
		}
		return m, nil
	}

	now := time.Now()
	burst := !m.lastKey.IsZero() && now.Sub(m.lastKey) < pasteGap
	m.lastKey = now
	if k.Paste && m.editID == "" && m.onPaste(k) {
		return m, nil // multi-line paste: kept as one message, nothing is sent
	}

	switch s { // typing mode
	case "esc":
		if m.hasReply || m.editID != "" {
			if m.editID != "" {
				m.input.SetValue("")
			}
			m.hasReply = false
			m.editID = ""
			return m, nil
		}
		return m, m.leaveChat()
	case "tab":
		m.syncDraft()
		m.inChat = false
		m.input.Blur()
		return m, nil
	case "backspace":
		if m.input.Value() == "" && len(m.lines) > 0 && m.editID == "" {
			last := m.lines[len(m.lines)-1] // take the last pasted line back into the box
			m.lines = m.lines[:len(m.lines)-1]
			m.input.SetValue(last)
			m.input.CursorEnd()
			return m, nil
		}
	case "up":
		if m.input.Value() == "" && len(m.msgs) > 0 {
			m.selMsg = len(m.msgs) - 1
			m.input.Blur()
			return m, nil
		}
		m.scroll += 3
		return m, nil
	case "down":
		m.scroll = max(0, m.scroll-3)
		return m, nil
	case "pgup", "ctrl+u":
		m.scroll += page
		return m, nil
	case "pgdown", "ctrl+d":
		m.scroll = max(0, m.scroll-page)
		return m, nil
	case "end":
		m.scroll = 0
		return m, nil
	case "enter":
		if burst && m.editID == "" { // Enter inside a paste = new line, never "send"
			m.lines = append(m.lines, m.input.Value())
			m.input.SetValue("")
			return m, nil
		}
		return m, m.submit()
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	cmds := []tea.Cmd{cmd}
	v := m.input.Value()
	if m.editID == "" && v != before && v != "" && !strings.HasPrefix(v, "/") && time.Since(m.lastTyping) > 4*time.Second {
		m.lastTyping = time.Now()
		cmds = append(cmds, m.typingCmd(m.cur, true))
	}
	return m, tea.Batch(cmds...)
}

// ---- contacts picker

func (m *tuiModel) openContacts() tea.Cmd {
	m.contactsMode = true
	m.inChat = false
	m.input.Blur()
	m.filtering = false
	m.filter.SetValue("")
	m.filter.Blur()
	m.res, m.rsel = nil, 0
	m.cfilter.SetValue("")
	m.cfilter.Focus()
	m.csel = 0
	m.refilterContacts()
	return tea.Batch(textinput.Blink, m.loadContacts())
}

func (m *tuiModel) closeContacts() {
	m.contactsMode = false
	m.cfilter.Blur()
	m.cfilter.SetValue("")
}

func (m *tuiModel) refilterContacts() {
	q := strings.TrimSpace(m.cfilter.Value())
	if q == "" {
		m.cshown = m.contacts
		m.csel = clamp(m.csel, 0, max(0, len(m.cshown)-1))
		return
	}
	type scored struct {
		c Contact
		s int
	}
	var tmp []scored
	qd := digitsOnly(q)
	for _, c := range m.contacts {
		sc := fuzzyScore(q, c.Name)
		if sc == 0 && len(qd) >= 2 && strings.Contains(c.Phone, qd) {
			sc = 50
		}
		if sc > 0 {
			tmp = append(tmp, scored{c, sc})
		}
	}
	sort.SliceStable(tmp, func(i, j int) bool { return tmp[i].s > tmp[j].s })
	out := make([]Contact, 0, len(tmp)+1)
	// a number that is not in the address book can still be messaged
	if isPhoneLike(q) {
		exists := false
		for _, t := range tmp {
			if t.c.Phone == qd {
				exists = true
			}
		}
		if !exists {
			out = append(out, Contact{JID: qd + "@s.whatsapp.net", Phone: qd})
		}
	}
	for _, t := range tmp {
		out = append(out, t.c)
	}
	m.cshown = out
	m.csel = clamp(m.csel, 0, max(0, len(m.cshown)-1))
}

func (m *tuiModel) moveCSel(d int) {
	if len(m.cshown) == 0 {
		return
	}
	m.csel = clamp(m.csel+d, 0, len(m.cshown)-1)
}

func contactTitle(c Contact) string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	if c.Phone != "" {
		return "+" + c.Phone
	}
	return userPart(c.JID)
}

func (m *tuiModel) openContact(i int) tea.Cmd {
	if i < 0 || i >= len(m.cshown) {
		return nil
	}
	c := m.cshown[i]
	m.cfilter.SetValue("")
	return m.openJID(c.JID, contactTitle(c))
}

func (m *tuiModel) onContactsKey(k tea.KeyMsg, s string) (tea.Model, tea.Cmd) {
	switch s {
	case "esc":
		m.closeContacts()
		return m, nil
	case "enter":
		return m, m.openContact(m.csel)
	case "up":
		m.moveCSel(-1)
		return m, nil
	case "down":
		m.moveCSel(1)
		return m, nil
	case "pgup":
		m.moveCSel(-5)
		return m, nil
	case "pgdown":
		m.moveCSel(5)
		return m, nil
	}
	var cmd tea.Cmd
	m.cfilter, cmd = m.cfilter.Update(k)
	m.refilterContacts()
	return m, cmd
}

// ---- mouse / touch

// paneSplit returns the width of the left and right panes and the x where the right pane starts.
func (m *tuiModel) paneSplit() (lw, rw, rx0 int) {
	pw := m.paneWidths()
	lw, rw = pw[0], pw[1]
	rx0 = m.w - rw
	return
}

// tabSwipeOK: the plain chat list accepts swipes (and so decides a tap on release).
func (m *tuiModel) tabSwipeOK() bool { return !m.contactsMode && !m.searching() }

const swipeCols = 5 // a horizontal drag this wide (in cells) switches the tab

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// onMouseRelease finishes a touch that started on the chat list:
// a wide horizontal drag switches the tab, a short press is a tap.
func (m *tuiModel) onMouseRelease(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.pressing {
		return m, nil
	}
	m.pressing = false
	dx, dy := msg.X-m.pressX, msg.Y-m.pressY
	if absInt(dx) >= swipeCols && absInt(dx) >= 2*absInt(dy) {
		if dx < 0 {
			m.switchTab(1) // finger moved left -> next tab
		} else {
			m.switchTab(-1) // finger moved right -> previous tab
		}
		return m, nil
	}
	if absInt(dy) >= 3 && absInt(dy) > absInt(dx) {
		return m, nil // a vertical drag is not a tap
	}
	lw, _, _ := m.paneSplit()
	return m.clickLeft(m.pressX, m.pressY, lw)
}

func (m *tuiModel) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action == tea.MouseActionRelease {
		return m.onMouseRelease(msg)
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if m.showHelp {
		if msg.Button == tea.MouseButtonLeft {
			m.showHelp = false
		}
		return m, nil
	}
	if m.showDash {
		if msg.Button == tea.MouseButtonLeft {
			if mm, cmd, ok := m.dashClick(msg.X, msg.Y); ok {
				return mm, cmd
			}
			m.showDash = false
		}
		return m, nil
	}
	if m.showFiles {
		return m.onFilesMouse(msg)
	}
	lw, rw, rx0 := m.paneSplit()
	inLeft := lw > 0 && msg.X < lw
	inRight := rw > 0 && msg.X >= rx0
	switch msg.Button {
	case tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight:
		if inLeft && m.tabSwipeOK() { // terminals that report a sideways swipe as a wheel
			if msg.Button == tea.MouseButtonWheelLeft {
				m.switchTab(-1)
			} else {
				m.switchTab(1)
			}
		}
		return m, nil
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		d := 1
		if msg.Button == tea.MouseButtonWheelUp {
			d = -1
		}
		switch {
		case inLeft && m.contactsMode:
			m.moveCSel(d * 2)
		case inLeft && m.searching():
			m.moveRes(d * 2)
		case inLeft:
			m.moveSel(d * 2)
		case inRight && m.cur != "":
			if m.selMsg >= 0 {
				m.selMsg = clamp(m.selMsg+d, 0, len(m.msgs)-1)
			} else if d < 0 {
				m.scroll += 3
			} else {
				m.scroll = max(0, m.scroll-3)
			}
		}
		return m, nil
	case tea.MouseButtonLeft:
		if inLeft {
			if m.tabSwipeOK() {
				m.pressing, m.pressX, m.pressY = true, msg.X, msg.Y
				return m, nil // tap or swipe is decided on release
			}
			return m.clickLeft(msg.X, msg.Y, lw)
		}
		if inRight {
			return m.clickRight(msg.X-rx0, msg.Y, rw)
		}
	}
	return m, nil
}

// openJIDByChat opens a chat that is (or may be) in the chat list.
func (m *tuiModel) openJIDByChat(jid string) tea.Cmd {
	for _, c := range m.chats {
		if c.JID == jid {
			return m.openJID(c.JID, c.Title())
		}
	}
	return m.openJID(jid, userPart(jid))
}

func (m *tuiModel) clickLeft(x, y, w int) (tea.Model, tea.Cmd) {
	h := m.h
	if m.contactsMode {
		switch {
		case y == 1: // "‹ Contacts" bar closes the picker
			m.closeContacts()
		case y >= 2 && y < h-1:
			perPage, top := m.contactGeom(h)
			i := top + (y-2)/2
			if (y-2)/2 < perPage {
				return m, m.openContact(i)
			}
		}
		return m, nil
	}
	if m.searching() {
		switch {
		case fabHit(x, y, w, h-1) == fabContacts:
			return m, m.openContacts()
		case fabHit(x, y, w, h-1) == fabFiles:
			return m, m.openFiles()
		case y >= 2 && y < h-1:
			perPage, top := m.resGeom(h)
			slot := (y - 2) / 2
			if slot < perPage && top+slot < len(m.res) {
				m.rsel = top + slot
				return m, m.openRes()
			}
		}
		return m, nil
	}
	switch {
	case fabHit(x, y, w, h-navRows) == fabContacts:
		return m, m.openContacts()
	case fabHit(x, y, w, h-navRows) == fabFiles:
		return m, m.openFiles()
	case y == 0 && !m.filtering:
		m.showDash = true // tap the "WhatsApp" bar for the home dashboard
	case y >= h-navRows: // bottom tab bar
		for _, p := range m.navCells(w) {
			if x >= p.x0 && x < p.x1 {
				m.setTab(p.kind)
			}
		}
	case y >= 1 && y < h-navRows:
		perPage, top := m.listGeom(h)
		slot := (y - 1) / 2
		if slot < perPage && top+slot < len(m.shown) {
			m.sel = top + slot
			m.selJID = m.shown[m.sel].JID
			return m, m.openSel()
		}
	}
	return m, nil
}

func (m *tuiModel) clickRight(x, y, w int) (tea.Model, tea.Cmd) {
	h := m.h
	if m.cur == "" { // home dashboard: tap a chat, card or button
		if mm, cmd, ok := m.dashClick(x, y); ok {
			return mm, cmd
		}
		return m, nil
	}
	if !m.inChat { // wide screen: tapping the chat pane gives it focus
		m.inChat = true
		m.input.Focus()
		return m, textinput.Blink
	}
	switch {
	case y == 0:
		if m.w < 70 && x <= 4 {
			return m, m.leaveChat() // the "‹" back arrow
		}
	case y == h-1: // tapping the input box returns from message-picking to typing
		if m.selMsg >= 0 || m.picker || m.confirmDel {
			m.picker, m.confirmDel = false, false
			return m, m.exitSelect()
		}
	case y == h-2:
		if m.hasReply || m.editID != "" { // tap the reply / edit bar to cancel it
			if m.editID != "" {
				m.input.SetValue("")
			}
			m.hasReply = false
			m.editID = ""
		}
	case y >= 1 && y < h-2:
		if len(m.msgs) == 0 {
			return m, nil
		}
		avail := h - 3
		lay := m.layoutMsgs(w)
		scroll := clamp(m.scroll, 0, max(0, len(lay.lines)-avail))
		end := len(lay.lines) - scroll
		start := max(0, end-avail)
		pad := avail - (end - start)
		line := start + (y - 1 - pad)
		if line < start || line >= end {
			return m, nil
		}
		for i := range m.msgs {
			if line >= lay.start[i] && line < lay.end[i] {
				m.selMsg = i // tap a message to pick it (reply / react / edit …)
				m.input.Blur()
				break
			}
		}
	}
	return m, nil
}

func (m *tuiModel) pick(rest string) (Message, string, error) {
	f := strings.Fields(rest)
	if len(f) == 0 {
		return Message{}, "", fmt.Errorf("give a message number, e.g. 3")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(f[0], "#"))
	if err != nil || n < 1 || n > len(m.msgs) {
		return Message{}, "", fmt.Errorf("no message %s", f[0])
	}
	body := strings.TrimSpace(strings.TrimPrefix(rest, f[0]))
	return m.msgs[n-1], body, nil
}

func (m *tuiModel) submit() tea.Cmd {
	if m.cur == "" {
		return nil
	}
	text := strings.TrimSpace(m.input.Value())
	if m.editID == "" { // pasted lines + the box = one single message
		text = strings.TrimSpace(strings.Join(append(append([]string{}, m.lines...), m.input.Value()), "\n"))
	}
	if text == "" {
		return nil
	}
	chat, be := m.cur, m.be

	if m.editID != "" {
		id := m.editID
		m.editID = ""
		m.input.SetValue("")
		return m.act(func() (string, error) {
			return "edited", be.Edit(EditArgs{Chat: chat, ID: id, Text: text})
		})
	}
	m.input.SetValue("")
	multi := len(m.lines) > 0
	m.lines = nil
	m.syncDraft() // sent, so the saved draft is gone
	if !multi && strings.HasPrefix(text, "/") {
		return m.slash(text)
	}
	m.scroll = 0
	replyID := ""
	if m.hasReply {
		replyID = m.reply.ID
		m.hasReply = false
	}
	return tea.Batch(
		m.typingCmd(chat, false),
		m.act(func() (string, error) {
			_, err := be.Send(SendArgs{Chat: chat, Text: text, ReplyTo: replyID})
			return "", err
		}),
	)
}

func (m *tuiModel) slash(text string) tea.Cmd {
	chat, be := m.cur, m.be
	fields := strings.Fields(text)
	cmd := strings.ToLower(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	fail := func(err error) tea.Cmd {
		m.status = "⚠ " + err.Error()
		return nil
	}
	switch cmd {
	case "/help", "/?":
		m.showHelp = true
	case "/pin":
		if m.togglePin(chat) {
			m.status = "★ pinned to the top"
		} else {
			m.status = "unpinned"
		}
	case "/theme":
		name := strings.ToLower(strings.TrimSpace(rest))
		if !applyTheme(name) {
			return fail(fmt.Errorf("themes: %s", strings.Join(themeNames(), ", ")))
		}
		c := loadConfig()
		c.Theme = name
		_ = c.Save()
		m.restyle()
		m.status = "theme: " + name
	case "/file":
		parts := strings.Fields(rest)
		if len(parts) == 0 {
			return fail(fmt.Errorf("usage: /file path [caption]"))
		}
		path := expandHome(parts[0])
		caption := strings.TrimSpace(strings.TrimPrefix(rest, parts[0]))
		return m.act(func() (string, error) {
			_, err := be.SendFile(FileArgs{Chat: chat, Path: path, Caption: caption})
			if err != nil {
				return "", err
			}
			return "file sent", nil
		})
	case "/get":
		mm, _, err := m.pick(rest)
		if err != nil {
			return fail(err)
		}
		return m.doDownload(mm)
	case "/r", "/reply":
		mm, body, err := m.pick(rest)
		if err != nil || body == "" {
			return fail(fmt.Errorf("usage: /r N text"))
		}
		return m.act(func() (string, error) {
			_, err := be.Send(SendArgs{Chat: chat, Text: body, ReplyTo: mm.ID})
			return "", err
		})
	case "/react":
		mm, body, err := m.pick(rest)
		if err != nil {
			return fail(err)
		}
		return m.doReact(mm, body)
	case "/edit":
		mm, body, err := m.pick(rest)
		if err != nil || body == "" {
			return fail(fmt.Errorf("usage: /edit N new text"))
		}
		return m.act(func() (string, error) {
			return "edited", be.Edit(EditArgs{Chat: chat, ID: mm.ID, Text: body})
		})
	case "/del", "/rm":
		mm, _, err := m.pick(rest)
		if err != nil {
			return fail(err)
		}
		return m.doDelete(mm)
	default:
		m.status = "⚠ unknown command " + cmd + " (try /help)"
	}
	return nil
}

// ================================================================ view

// paneWidths returns {left, right}; one of them is 0 on narrow screens (phones).
func (m *tuiModel) paneWidths() [2]int {
	if m.w < 70 {
		if m.inChat {
			return [2]int{0, m.w}
		}
		return [2]int{m.w, 0}
	}
	l := clamp(m.w/3, 26, 38)
	return [2]int{l, m.w - l - 1}
}

func (m *tuiModel) View() string {
	if m.w == 0 || m.h == 0 {
		return "loading…"
	}
	if m.h < 8 {
		return "terminal too small"
	}
	if m.showHelp {
		return m.renderHelp()
	}
	if m.showDash {
		return m.renderDashScreen()
	}
	if m.showFiles {
		return m.renderFiles()
	}
	pw := m.paneWidths()
	lw, rw := pw[0], pw[1]
	var left, right []string
	if lw > 0 {
		left = m.renderList(lw, m.h)
	}
	if rw > 0 {
		right = m.renderChat(rw, m.h)
	}
	rows := make([]string, m.h)
	sep := render(th.bg, 1, seg{t: "│", fg: th.sel})
	for i := range rows {
		switch {
		case lw > 0 && rw > 0:
			rows[i] = left[i] + sep + right[i]
		case lw > 0:
			rows[i] = left[i]
		default:
			rows[i] = right[i]
		}
	}
	return strings.Join(rows, "\n")
}

func (m *tuiModel) renderHelp() string {
	w := m.w
	var rows []string
	title := func(t string) {
		rows = append(rows, bgr(w, seg{t: " " + t, fg: th.accent, bold: true}))
	}
	line := func(k, d string) {
		budget := max(0, w-3-12-1)
		rows = append(rows, bgr(w,
			seg{t: "   " + runewidth.FillRight(k, 12), fg: th.text, bold: true},
			seg{t: trunc(d, budget), fg: th.dim}))
	}
	blank := func() { rows = append(rows, blankRow(w)) }

	rows = append(rows, render(th.bar, w, seg{t: " wacli — help", fg: th.text, bold: true}))
	blank()
	title("Chat list")
	line("⏎", "open chat")
	line("← → 1-4", "Chats Groups Channels Unread")
	line("u", "unread tab on / off")
	line("p", "pin / unpin chat ★")
	line("c  or  n", "contacts · new chat")
	line("f", "files · open / send a download")
	line("d", "home dashboard")
	line("t", "next theme")
	line("j k  ↑ ↓", "move")
	line("/", "search chats · contacts · messages")
	line("q", "quit")
	blank()
	title("Touch")
	line("tap", "open chat · tab · button")
	line("tap 📁", "browse files · open or send")
	line("swipe ↕", "scroll list or messages")
	line("swipe ← →", "next / previous tab (chat list)")
	line("tap message", "pick it (reply, react…)")
	blank()
	title("In a chat")
	line("⏎", "send (pasted text = 1 message)")
	line("drafts", "unsent text is kept per chat")
	line("↑ (empty box)", "pick a message")
	line("PgUp PgDn", "scroll · End = latest")
	line("Esc", "back / cancel reply")
	blank()
	title("On a picked message")
	line("r  or  ⏎", "reply")
	line("e", "edit (yours)")
	line("d", "delete (yours)")
	line("g", "save photo/file")
	line("+", "react 👍❤️😂😮😢🙏")
	blank()
	title("Commands")
	line("/file path", "send a file [caption]")
	line("/pin", "pin / unpin this chat")
	line("/theme name", strings.Join(themeNames(), " "))
	blank()
	rows = append(rows, bgr(w,
		seg{t: " ✓ sent  ✓✓ delivered  ", fg: th.dim},
		seg{t: "✓✓", fg: th.tick},
		seg{t: " read", fg: th.dim}))
	blank()
	rows = append(rows, bgr(w, seg{t: " press any key to close", fg: th.dim, ital: true}))
	for len(rows) < m.h {
		rows = append(rows, blankRow(w))
	}
	if len(rows) > m.h {
		rows = rows[:m.h]
	}
	return strings.Join(rows, "\n")
}

type tabPos struct {
	kind, x0, x1 int
	label        string
}

// tabLayout places the tabs; it falls back to shorter names when the pane is narrow.
func (m *tuiModel) tabLayout(w int) []tabPos {
	var out []tabPos
	for mode := 0; mode < 3; mode++ {
		out = out[:0]
		used, fits := 1, true
		for _, k := range m.tabList() {
			name := tabName(k)
			if mode >= 1 {
				name = tabShort(k)
			}
			label := " " + name
			if n := m.tabUnread(k); n > 0 {
				label += " " + unreadLabel(n)
			}
			label += " "
			lw := runewidth.StringWidth(label)
			if used+lw > w {
				fits = false
				break
			}
			out = append(out, tabPos{kind: k, x0: used, x1: used + lw, label: label})
			used += lw
			if mode < 2 {
				used++
			}
		}
		if fits {
			break
		}
	}
	return out
}

func (m *tuiModel) tabBar(w int) string {
	segs := []seg{}
	cur := 0
	for _, p := range m.tabLayout(w) {
		if p.x0 > cur {
			segs = append(segs, seg{t: spaces(p.x0 - cur)})
		}
		if p.kind == m.tab {
			segs = append(segs, seg{t: p.label, fg: th.bg, bg: th.accent, bold: true})
		} else {
			segs = append(segs, seg{t: p.label, fg: th.dim})
		}
		cur = p.x1
	}
	return bgr(w, segs...)
}

const navRows = 2 // height of the bottom tab bar: icon row + label row

func tabIcon(k int) string { return [...]string{"💬", "👥", "📢", "🔔"}[k] }

// navCells splits the bottom tab bar into one equal-width cell per tab.
func (m *tuiModel) navCells(w int) []tabPos {
	list := m.tabList()
	n := len(list)
	out := make([]tabPos, 0, n)
	for i, k := range list {
		out = append(out, tabPos{kind: k, x0: i * w / n, x1: (i + 1) * w / n})
	}
	return out
}

// navBar draws the WhatsApp-style bottom tab bar: an icon row (the active tab sits on a
// green pill, unread count as a badge) and a label row below it.
func (m *tuiModel) navBar(w int) (string, string) {
	var icons, labels []seg
	for _, p := range m.navCells(w) {
		cw := p.x1 - p.x0
		active := p.kind == m.tab

		pill := seg{t: " " + tabIcon(p.kind) + " ", fg: th.text}
		if active {
			pill.bg = th.me
		}
		badge := seg{}
		if n := m.tabUnread(p.kind); n > 0 {
			badge = seg{t: " " + unreadLabel(n) + " ", fg: th.bg, bg: th.accent, bold: true}
		}
		tw := segsWidth([]seg{pill, badge})
		if tw > cw { // too narrow for the badge
			badge = seg{}
			tw = segsWidth([]seg{pill})
		}
		left := max(0, (cw-tw)/2)
		icons = append(icons, seg{t: spaces(left)}, pill, badge, seg{t: spaces(max(0, cw-tw-left))})

		name := tabName(p.kind)
		if runewidth.StringWidth(name)+1 > cw {
			name = tabShort(p.kind)
		}
		name = trunc(name, cw)
		nw := runewidth.StringWidth(name)
		pad := max(0, (cw-nw)/2)
		lab := seg{t: name, fg: th.dim}
		if active {
			lab = seg{t: name, fg: th.text, bold: true}
		}
		labels = append(labels, seg{t: spaces(pad)}, lab, seg{t: spaces(max(0, cw-nw-pad))})
	}
	return render(th.bar, w, icons...), render(th.bar, w, labels...)
}

// ---- floating action buttons (WhatsApp style): two thick buttons stacked at the bottom
// right of the chat list - "Files" on top, "Contacts" (the main one) underneath.
const (
	fabW     = 14 // button width
	fabH     = 3  // button height (rows) - thick, easy to tap
	fabGap   = 1  // empty rows between the two buttons
	fabPad   = 1  // empty column on each side of a button
	fabAreaW = fabW + 2*fabPad
	fabTotal = 2*fabH + fabGap
)

const (
	fabNone = iota
	fabContacts
	fabFiles
)

// fabOK: is there room for the buttons? bottom = first row below the button stack.
func fabOK(w, bottom int) bool { return w >= 28 && bottom-fabTotal >= 4 }

func fabTop(bottom int) int { return bottom - fabTotal }

// fabHit tells which button (if any) a tap at x,y landed on.
func fabHit(x, y, w, bottom int) int {
	if !fabOK(w, bottom) || x < w-fabAreaW || x >= w {
		return fabNone
	}
	top := fabTop(bottom)
	switch {
	case y >= top && y < top+fabH:
		return fabFiles
	case y >= bottom-fabH && y < bottom:
		return fabContacts
	}
	return fabNone
}

// fabLine draws one row (0..fabH-1) of a button, padded to fabAreaW columns.
func fabLine(kind, row int) string {
	fg, bg, label := th.bg, th.accent, "+ Contacts"
	if kind == fabFiles {
		fg, bg, label = th.text, th.sel, "📁 Files"
	}
	var body []seg
	if row == fabH/2 {
		lw := runewidth.StringWidth(label)
		l := max(0, (fabW-lw)/2)
		body = []seg{
			{t: spaces(l), bg: bg},
			{t: label, fg: fg, bg: bg, bold: true},
			{t: spaces(max(0, fabW-l-lw)), bg: bg},
		}
	} else {
		body = []seg{{t: spaces(fabW), bg: bg}}
	}
	segs := append([]seg{{t: spaces(fabPad)}}, body...)
	segs = append(segs, seg{t: spaces(fabPad)})
	return render(th.bg, fabAreaW, segs...)
}

// fabRowW is the width a list row may use: rows beside the buttons are narrower.
func fabRowW(w, r, bottom int) int {
	if fabOK(w, bottom) && r >= fabTop(bottom) && r < bottom {
		return w - fabAreaW
	}
	return w
}

// overlayFabs appends the button column to the (already narrowed) rows beside it.
func overlayFabs(rows []string, w, bottom int) {
	if !fabOK(w, bottom) {
		return
	}
	top := fabTop(bottom)
	for r := top; r < bottom; r++ {
		k := r - top
		switch {
		case k < fabH:
			rows[r] += fabLine(fabFiles, k)
		case k >= fabH+fabGap:
			rows[r] += fabLine(fabContacts, k-fabH-fabGap)
		default:
			rows[r] += blankRow(fabAreaW)
		}
	}
}

// listGeom: how many chat rows fit, and the index of the first one shown.
func (m *tuiModel) listGeom(h int) (perPage, top int) {
	perPage = max(1, (h-1-navRows)/2) // header on top, tab bar at the bottom (buttons float over the list)
	if len(m.shown) > perPage {
		top = clamp(m.sel-perPage/2, 0, len(m.shown)-perPage)
	}
	return
}

func (m *tuiModel) contactGeom(h int) (perPage, top int) {
	perPage = max(1, (h-3)/2)
	if len(m.cshown) > perPage {
		top = clamp(m.csel-perPage/2, 0, len(m.cshown)-perPage)
	}
	return
}

func (m *tuiModel) renderList(w, h int) []string {
	if m.contactsMode {
		return m.renderContacts(w, h)
	}
	rows := make([]string, h)
	for i := range rows {
		rows[i] = blankRow(fabRowW(w, i, h-navRows))
	}
	dotFg := lipgloss.Color("#F15C6D")
	if m.online {
		dotFg = th.accent
	}
	if m.filtering {
		v := m.filter.View()
		if pad := w - lipgloss.Width(v); pad > 0 {
			v += blankRow(pad)
		}
		rows[0] = v
	} else {
		rows[0] = render(th.bar, w,
			seg{t: " ●", fg: dotFg},
			seg{t: " WhatsApp", fg: th.text, bold: true},
			seg{t: spaces(max(1, w-13))},
			seg{t: "⋮ ", fg: th.text, bold: true})
	}
	if m.searching() {
		return m.renderResults(rows, w, h)
	}
	if len(m.shown) == 0 {
		t1, t2 := " Nothing here yet", " New messages show up here"
		if m.tab == 3 {
			t1, t2 = " No unread chats 🎉", " You're all caught up"
		}
		rows[1] = bgr(w, seg{t: trunc(t1, w), fg: th.dim})
		rows[2] = bgr(w, seg{t: trunc(t2, w), fg: th.dim})
	}
	perPage, top := m.listGeom(h)
	for i := 0; i < perPage; i++ {
		idx := top + i
		if idx >= len(m.shown) {
			break
		}
		r := 1 + i*2
		if r+1 >= h-navRows {
			break
		}
		l1, l2 := m.listItem(m.shown[idx], idx == m.sel, w)
		if rw := fabRowW(w, r+1, h-navRows); rw != w { // row(s) beside the floating buttons
			n1, n2 := m.listItem(m.shown[idx], idx == m.sel, rw)
			if fabRowW(w, r, h-navRows) != w {
				l1 = n1
			}
			l2 = n2
		}
		rows[r], rows[r+1] = l1, l2
	}
	// WhatsApp-style tab bar at the very bottom; the two buttons float above it, bottom right
	rows[h-2], rows[h-1] = m.navBar(w)
	overlayFabs(rows, w, h-navRows)
	return rows
}

func (m *tuiModel) renderContacts(w, h int) []string {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = blankRow(w)
	}
	v := m.cfilter.View()
	if pad := w - lipgloss.Width(v); pad > 0 {
		v += blankRow(pad)
	}
	rows[0] = v
	title := " ‹ Contacts"
	if n := len(m.contacts); n > 0 {
		title += fmt.Sprintf("  %d", n)
	}
	rows[1] = render(th.bar, w, seg{t: trunc(title, w), fg: th.accent, bold: true})

	if len(m.cshown) == 0 {
		msg := " Loading contacts…"
		if len(m.contacts) > 0 || strings.TrimSpace(m.cfilter.Value()) != "" {
			msg = " No match — type a full number to message it"
		}
		rows[2] = bgr(w, seg{t: trunc(msg, w), fg: th.dim})
	}
	perPage, top := m.contactGeom(h)
	for i := 0; i < perPage; i++ {
		idx := top + i
		if idx >= len(m.cshown) {
			break
		}
		r := 2 + i*2
		if r+1 >= h {
			break
		}
		rows[r], rows[r+1] = m.contactItem(m.cshown[idx], idx == m.csel, w)
	}
	rows[h-1] = hintRow(w, "⏎", "chat", "Esc", "back")
	return rows
}

func (m *tuiModel) contactItem(c Contact, selected bool, w int) (string, string) {
	bg := th.bg
	if selected {
		bg = th.sel
	}
	name := clean(contactTitle(c))
	av := runewidth.FillRight(runewidth.Truncate(initials(strings.TrimPrefix(name, "+")), 2, ""), 2)
	l1 := render(bg, w,
		seg{t: " "},
		seg{t: av, fg: "#FFFFFF", bg: avatarBgs[hashIdx(c.JID, len(avatarBgs))], bold: true},
		seg{t: " "},
		seg{t: trunc(name, max(1, w-5)), fg: th.text, bold: true},
	)
	sub := ""
	if strings.TrimSpace(c.Name) != "" && c.Phone != "" {
		sub = "+" + c.Phone
	} else if strings.TrimSpace(c.Name) == "" {
		sub = "Message this number"
	}
	l2 := render(bg, w,
		seg{t: "    "},
		seg{t: trunc(sub, max(1, w-5)), fg: th.dim},
	)
	return l1, l2
}

func (m *tuiModel) listItem(c Chat, selected bool, w int) (string, string) {
	bg := th.bg
	if selected {
		bg = th.sel
	}
	name := clean(c.Title())
	av := runewidth.FillRight(runewidth.Truncate(initials(name), 2, ""), 2)
	tm := listTime(c.LastTS)
	tmW := runewidth.StringWidth(tm)
	nameFg, tmFg := th.text, th.dim
	if c.Unread > 0 {
		tmFg = th.accent
	}
	starW, star := 0, ""
	if m.pinned[c.JID] {
		starW, star = 2, " ★"
	}
	nm := trunc(name, max(1, w-5-tmW-1-starW))
	gap := max(1, w-4-runewidth.StringWidth(nm)-starW-tmW-1)
	l1 := render(bg, w,
		seg{t: " "},
		seg{t: av, fg: "#FFFFFF", bg: avatarBgs[hashIdx(c.JID, len(avatarBgs))], bold: true},
		seg{t: " "},
		seg{t: nm, fg: nameFg, bold: c.Unread > 0},
		seg{t: star, fg: warnColor},
		seg{t: spaces(gap)},
		seg{t: tm, fg: tmFg},
	)

	badge := ""
	if c.Unread > 99 {
		badge = " 99+ "
	} else if c.Unread > 0 {
		badge = fmt.Sprintf(" %d ", c.Unread)
	}
	bw := runewidth.StringWidth(badge)
	pvText := strings.ReplaceAll(clean(prettyPreview(c.LastText)), "\n", " ")
	pvFg, pvItal := th.dim, false
	draftPfx := ""
	if until, ok := m.typingIn[c.JID]; ok && time.Now().Before(until) {
		pvText, pvFg, pvItal = "typing…", th.accent, true // live typing indicator
	} else if d := m.draftFor(c.JID); d != "" {
		draftPfx = "Draft: " // unsent text, in red
		pvText = strings.ReplaceAll(clean(d), "\n", " ")
	}
	pfxW := runewidth.StringWidth(draftPfx)
	pv := trunc(pvText, max(1, w-4-bw-1-pfxW))
	gap2 := max(0, w-4-pfxW-runewidth.StringWidth(pv)-bw-1)
	l2 := render(bg, w,
		seg{t: "    "},
		seg{t: draftPfx, fg: draftColor},
		seg{t: pv, fg: pvFg, ital: pvItal},
		seg{t: spaces(gap2)},
		seg{t: badge, fg: th.bg, bg: th.accent, bold: true},
	)
	return l1, l2
}

func presSeg(p PresenceInfo, typing bool) seg {
	switch {
	case typing:
		return seg{t: "typing…", fg: th.accent, ital: true}
	case !p.Known:
		return seg{}
	case p.Online:
		return seg{t: "● online", fg: th.accent}
	case p.LastSeen > 0:
		return seg{t: "last seen " + fmtTime(p.LastSeen), fg: th.dim}
	}
	return seg{t: "offline", fg: th.dim}
}

func (m *tuiModel) chatHeader(w int) string {
	if m.cur == "" {
		return render(th.bar, w, seg{t: " wacli", fg: th.text, bold: true})
	}
	lead := " "
	if m.w < 70 {
		lead = " ‹ "
	}
	right := presSeg(m.pres, m.typing)
	rw := runewidth.StringWidth(right.t)
	if rw > 0 {
		rw++ // trailing space
	}
	name := trunc(clean(m.curName), max(1, w-runewidth.StringWidth(lead)-3-rw-1))
	av := runewidth.FillRight(runewidth.Truncate(initials(m.curName), 2, ""), 2)
	left := []seg{
		{t: lead, fg: th.accent, bold: true},
		{t: av, fg: "#FFFFFF", bg: avatarBgs[hashIdx(m.cur, len(avatarBgs))], bold: true},
		{t: " "},
		{t: name, fg: th.text, bold: true},
	}
	gap := max(1, w-segsWidth(left)-rw)
	segs := append(left, seg{t: spaces(gap)})
	if right.t != "" {
		segs = append(segs, right, seg{t: " "})
	}
	return render(th.bar, w, segs...)
}

func (m *tuiModel) contextRow(w int) string {
	one := func(fg lipgloss.Color, text string) string {
		return bgr(w, seg{t: " " + trunc(clean(text), w-2), fg: fg})
	}
	switch {
	case m.confirmDel:
		return one(warnColor, "Delete for everyone?  y = yes · any other key = cancel")
	case m.picker:
		return one(th.accent, "React:  1👍 2❤️ 3😂 4😮 5😢 6🙏   Esc cancel")
	case m.hasReply:
		who := m.reply.SenderName
		if m.reply.FromMe {
			who = "You"
		} else if who == "" {
			who = userPart(m.reply.Sender)
		}
		txt := strings.ReplaceAll(prettyPreview(previewOf(&m.reply)), "\n", " ")
		return bgr(w,
			seg{t: " ↪ ", fg: th.accent, bold: true},
			seg{t: trunc(clean(who+": "+txt), max(1, w-6)), fg: th.dim},
			seg{t: "  ✕", fg: th.dim})
	case m.editID != "":
		return one(th.accent, "✎ Editing — Enter saves · Esc cancels")
	case m.status != "":
		fg := th.dim
		if strings.HasPrefix(m.status, "⚠") {
			fg = warnColor
		}
		return one(fg, m.status)
	case len(m.lines) > 0 && m.selMsg < 0:
		return one(th.accent, fmt.Sprintf("📋 %d lines pasted · sends as ONE message · Enter sends · ⌫ edits", len(m.lines)+1))
	case m.selMsg >= 0:
		return hintRow(w, "r", "reply", "e", "edit", "d", "del", "g", "save", "+", "react", "Esc", "back")
	case m.scroll > 0:
		return one(th.accent, "↓ scrolled up — press End for latest")
	}
	return hintRow(w, "↑", "pick message", "/file", "send file", "/help", "help")
}

func (m *tuiModel) renderChat(w, h int) []string {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = blankRow(w)
	}
	rows[0] = m.chatHeader(w)
	avail := h - 3

	if m.cur == "" {
		for i, l := range m.dashLines(w) {
			if 1+i < h-1 {
				rows[1+i] = l
			}
		}
	} else if len(m.msgs) == 0 {
		rows[avail/2+1] = bgr(w, seg{t: trunc("   No messages yet — say hi 👋", w), fg: th.dim})
	} else {
		lay := m.layoutMsgs(w)
		lines := lay.lines
		if m.selMsg >= 0 && m.selMsg < len(lay.start) {
			s, e := lay.start[m.selMsg], lay.end[m.selMsg]
			endWin := len(lines) - m.scroll
			startWin := endWin - avail
			if e > endWin {
				m.scroll = len(lines) - e
			} else if s < startWin {
				m.scroll = len(lines) - (s + avail)
			}
		}
		m.scroll = clamp(m.scroll, 0, max(0, len(lines)-avail))
		end := len(lines) - m.scroll
		start := max(0, end-avail)
		vis := lines[start:end]
		pad := avail - len(vis)
		for i := 0; i < avail; i++ {
			if j := i - pad; j >= 0 && j < len(vis) {
				rows[1+i] = vis[j]
			}
		}
	}
	rows[h-2] = m.contextRow(w)
	if m.cur != "" {
		v := m.input.View()
		if pad := w - lipgloss.Width(v); pad > 0 {
			v += blankRow(pad)
		}
		rows[h-1] = v
	}
	return rows
}

type msgLayout struct {
	lines      []string
	start, end []int
}

func (m *tuiModel) layoutMsgs(w int) msgLayout {
	lay := msgLayout{start: make([]int, len(m.msgs)), end: make([]int, len(m.msgs))}
	byID := make(map[string]Message, len(m.msgs))
	for _, mm := range m.msgs {
		byID[mm.ID] = mm
	}
	group := strings.HasSuffix(m.cur, "@g.us")
	prevSender, prevDay := "", ""
	var prevTS int64
	for i, mm := range m.msgs {
		day := dayLabel(mm.TS)
		newDay := day != prevDay
		if newDay {
			if len(lay.lines) > 0 {
				lay.lines = append(lay.lines, blankRow(w))
			}
			lay.lines = append(lay.lines, centerPill(day, w))
			prevDay = day
		}
		tail := newDay || mm.Sender != prevSender || mm.TS-prevTS > 300
		if i > 0 && !newDay {
			lay.lines = append(lay.lines, blankRow(w)) // one empty row between every two messages
		}
		lay.start[i] = len(lay.lines)
		lay.lines = append(lay.lines, m.bubble(mm, w, group, i == m.selMsg, tail, byID)...)
		lay.end[i] = len(lay.lines)
		prevSender, prevTS = mm.Sender, mm.TS
	}
	return lay
}

func centerPill(text string, w int) string {
	t := " " + text + " "
	return bgr(w,
		seg{t: spaces((w - runewidth.StringWidth(t)) / 2)},
		seg{t: t, fg: th.dim, bg: th.pill})
}

// bubble draws one WhatsApp-style message: seamless rounded corners (plain coloured
// cells), own messages on the right, a sharp "tail" corner on the first message of a run.
func (m *tuiModel) bubble(mm Message, w int, group, selected, tail bool, byID map[string]Message) []string {
	maxInner := clamp(w*75/100-2, 14, max(14, w-6))
	bb := th.them
	if mm.FromMe {
		bb = th.me
	}
	var rows [][]seg

	if group && !mm.FromMe {
		name := firstNonEmpty(mm.SenderName, userPart(mm.Sender))
		rows = append(rows, []seg{{t: trunc(clean(name), maxInner), fg: senderColors[hashIdx(mm.Sender, len(senderColors))], bold: true}})
	}
	if mm.ReplyTo != "" {
		q := "message"
		if qm, ok := byID[mm.ReplyTo]; ok {
			qw := qm.SenderName
			if qm.FromMe {
				qw = "You"
			} else if qw == "" {
				qw = userPart(qm.Sender)
			}
			q = qw + ": " + strings.ReplaceAll(prettyPreview(previewOf(&qm)), "\n", " ")
		}
		rows = append(rows, []seg{
			{t: "▎", fg: th.accent},
			{t: trunc(" "+clean(q), maxInner-1), fg: th.dim, ital: true},
		})
	}

	body := clean(mm.Text)
	bodyFg := th.text
	switch {
	case mm.Deleted:
		body, bodyFg = "🚫 This message was deleted", th.dim
	case kindLabel(mm) != "":
		if body != "" {
			body = kindLabel(mm) + "\n" + body
		} else {
			body = kindLabel(mm)
		}
	}
	if body != "" {
		for _, l := range wrapText(body, maxInner) {
			rows = append(rows, []seg{{t: l, fg: bodyFg, ital: mm.Deleted}})
		}
	}
	if mm.Reactions != "" {
		rows = append(rows, []seg{{t: trunc(mm.Reactions, maxInner)}})
	}

	// footer: [edited] 12:30 ✓✓  — placed on the last text row when it fits
	var foot []seg
	if mm.Edited && !mm.Deleted {
		foot = append(foot, seg{t: "edited ", fg: th.dim, ital: true})
	}
	foot = append(foot, seg{t: time.Unix(mm.TS, 0).Format("15:04"), fg: th.dim})
	if mm.FromMe {
		tick, tc := "✓", th.dim
		switch {
		case mm.Status >= 2:
			tick, tc = "✓✓", th.tick
		case mm.Status == 1:
			tick = "✓✓"
		}
		foot = append(foot, seg{t: " " + tick, fg: tc})
	}
	fw := segsWidth(foot)

	inner := 0
	for _, r := range rows {
		inner = max(inner, segsWidth(r))
	}
	merge := false
	if n := len(rows); n > 0 {
		if lw := segsWidth(rows[n-1]); lw+2+fw <= maxInner {
			merge = true
			inner = max(inner, lw+2+fw)
		}
	}
	if !merge {
		inner = max(inner, fw)
	}
	inner = clamp(inner, 4, maxInner)
	if merge {
		n := len(rows)
		last := append([]seg{}, rows[n-1]...)
		last = append(last, seg{t: spaces(inner - segsWidth(rows[n-1]) - fw)})
		rows[n-1] = append(last, foot...)
	} else {
		rows = append(rows, append([]seg{{t: spaces(inner - fw)}}, foot...))
	}

	total := inner + 2

	// Plain rectangular bubble made only of background-coloured cells (no half/quadrant
	// block glyphs - Termux fonts draw visible seams with those). One padding row on
	// top, none at the bottom, so a one-line message is 2 rows tall (2 selection bars).
	topRow := []seg{{t: spaces(total), bg: bb}}

	cores := [][]seg{topRow}
	for _, r := range rows {
		line := []seg{{t: " ", bg: bb}}
		for _, s := range r {
			if s.bg == "" {
				s.bg = bb
			}
			if s.fg == "" {
				s.fg = th.text
			}
			line = append(line, s)
		}
		if rw := segsWidth(r); rw < inner {
			line = append(line, seg{t: spaces(inner - rw), bg: bb})
		}
		line = append(line, seg{t: " ", bg: bb})
		cores = append(cores, line)
	}

	lead := " "
	if mm.FromMe {
		lead = spaces(max(1, w-total-1))
	}
	out := make([]string, 0, len(cores))
	for _, core := range cores {
		var segs []seg
		if selected {
			segs = append(segs, seg{t: "▌", fg: th.accent}, seg{t: lead[1:]})
		} else {
			segs = append(segs, seg{t: lead})
		}
		segs = append(segs, core...)
		out = append(out, render(th.bg, w, segs...))
	}
	return out
}

// ================================================================ home dashboard

type dashHit struct {
	y      int    // pane row
	x0, x1 int    // pane columns [x0, x1)
	jid    string // tap opens this chat ...
	act    string // ... or runs this action: new, search, theme, unread, pinned, drafts
}

// dashLogo is the big banner, used when the pane is wide and tall enough.
var dashLogo = []string{
	"╔══════════════════════════════════════════╗",
	"║ ██╗    ██╗ █████╗  ██████╗██╗     ██╗    ║",
	"║ ██║    ██║██╔══██╗██╔════╝██║     ██║    ║",
	"║ ██║ █╗ ██║███████║██║     ██║     ██║    ║",
	"║ ██║███╗██║██╔══██║██║     ██║     ██║    ║",
	"║ ╚███╔███╔╝██║  ██║╚██████╗███████╗██║    ║",
	"║  ╚══╝╚══╝ ╚═╝  ╚═╝ ╚═════╝╚══════╝╚═╝    ║",
	"╚══════════════════════════════════════════╝",
}

// dashLogoSmall is the 3-row banner for narrow or short screens.
var dashLogoSmall = []string{"╦ ╦╔═╗╔═╗╦  ╦", "║║║╠═╣║  ║  ║", "╚╩╝╩ ╩╚═╝╩═╝╩"}

// padCenter centres s inside w columns (cut to w if it is longer).
func padCenter(s string, w int) string {
	n := runewidth.StringWidth(s)
	if n >= w {
		return runewidth.Truncate(s, w, "")
	}
	l := (w - n) / 2
	return spaces(l) + s + spaces(w-n-l)
}

// dashLines draws the home dashboard: logo, status, stat cards, unread (or recent)
// chats and quick buttons. Rows are meant to start at pane row 1. Tappable spots are
// recorded in m.dashHits with pane-relative columns.
func (m *tuiModel) dashLines(w int) []string {
	m.dashHits = m.dashHits[:0]
	var rows []string
	limit := m.h - 3 // rows that stay visible in both the full-screen and the split layout
	compact := limit < 22

	cw := clamp(w-2, 20, 44) // content column, centred in the pane
	x0 := max(1, (w-cw)/2)

	add := func(segs ...seg) {
		rows = append(rows, bgr(w, append([]seg{{t: spaces(x0)}}, segs...)...))
	}
	blank := func() {
		if !compact {
			rows = append(rows, blankRow(w))
		}
	}
	text := func(fg lipgloss.Color, bold bool, t string) {
		add(seg{t: " " + trunc(t, max(1, cw-1)), fg: fg, bold: bold})
	}
	// hit marks columns a..b of the row that the NEXT add() call will draw.
	hit := func(a, b int, jid, act string) {
		m.dashHits = append(m.dashHits, dashHit{y: 1 + len(rows), x0: x0 + a, x1: x0 + b, jid: jid, act: act})
	}

	// ---- logo + status
	blank()
	if !compact && cw >= 44 && limit >= 34 {
		for i, l := range dashLogo {
			if i == 0 || i == len(dashLogo)-1 {
				add(seg{t: l, fg: th.dim})
				continue
			}
			r := []rune(l)
			add(
				seg{t: string(r[:1]), fg: th.dim},
				seg{t: string(r[1 : len(r)-1]), fg: th.accent, bold: true},
				seg{t: string(r[len(r)-1:]), fg: th.dim},
			)
		}
	} else {
		for _, l := range dashLogoSmall {
			add(seg{t: " " + l, fg: th.accent, bold: true})
		}
	}
	text(th.dim, false, "WhatsApp from your terminal")

	dot, state := lipgloss.Color("#F15C6D"), "offline"
	if m.online {
		dot, state = th.accent, "connected"
	}
	mode := "direct"
	if m.st.Mode == "daemon" {
		mode = "daemon"
	}
	add(seg{t: " ● ", fg: dot}, seg{t: trunc(state+" · "+mode, max(1, cw-3)), fg: th.text})

	who := strings.TrimSpace(m.st.PushName)
	if m.st.Phone != "" {
		if who != "" {
			who += " · "
		}
		who += "+" + m.st.Phone
	}
	if who != "" {
		text(th.dim, false, "  "+who)
	}

	// ---- numbers
	un := m.unreadChats()
	total := 0
	for _, c := range un {
		total += c.Unread
	}
	pins := 0
	for _, c := range m.chats {
		if m.pinned[c.JID] && chatKind(c.JID) >= 0 {
			pins++
		}
	}
	drafts := 0
	for _, d := range m.drafts {
		if strings.TrimSpace(d) != "" {
			drafts++
		}
	}

	// ---- stat cards (tap: unread tab, chat list, first draft)
	if !compact {
		blank()
		inner := cw - 4
		cwid := [3]int{inner / 3, inner / 3, inner - 2*(inner/3)}
		vals := [3]string{"✉ " + strconv.Itoa(total), "★ " + strconv.Itoa(pins), "✎ " + strconv.Itoa(drafts)}
		labs := [3]string{"unread", "pinned", "drafts"}
		acts := [3]string{"unread", "pinned", "drafts"}
		fgs := [3]lipgloss.Color{th.dim, th.dim, th.dim}
		if total > 0 {
			fgs[0] = th.accent
		}
		if pins > 0 {
			fgs[1] = warnColor
		}
		if drafts > 0 {
			fgs[2] = draftColor
		}
		border := func(l, mid, r string) {
			s := l
			for i := 0; i < 3; i++ {
				s += strings.Repeat("─", cwid[i])
				if i < 2 {
					s += mid
				} else {
					s += r
				}
			}
			add(seg{t: s, fg: th.dim})
		}
		border("┌", "┬", "┐")
		for r := 0; r < 2; r++ {
			segs := []seg{{t: "│", fg: th.dim}}
			x := 1
			for i := 0; i < 3; i++ {
				txt, fg, bold := vals[i], fgs[i], true
				if r == 1 {
					txt, fg, bold = labs[i], th.dim, false
				}
				segs = append(segs,
					seg{t: padCenter(txt, cwid[i]), fg: fg, bg: th.pill, bold: bold},
					seg{t: "│", fg: th.dim})
				hit(x, x+cwid[i], "", acts[i])
				x += cwid[i] + 1
			}
			add(segs...)
		}
		border("└", "┴", "┘")
	}

	// ---- unread chats (or the latest chats when everything is read)
	blank()
	list := un
	if len(un) > 0 {
		text(th.text, true, fmt.Sprintf("UNREAD  %d msgs · %d chats", total, len(un)))
	} else {
		list = nil
		for _, c := range m.chats {
			if chatKind(c.JID) >= 0 && c.LastTS > 0 {
				list = append(list, c)
			}
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].LastTS > list[j].LastTS })
		if len(list) > 0 {
			text(th.accent, true, "✓ ALL CAUGHT UP · RECENT")
		} else {
			text(th.accent, true, "✓ ALL CAUGHT UP")
		}
	}

	tail := 2 // buttons row + hint row
	if !compact {
		tail += 2 // blank rows above them
	}
	free := limit - len(rows) - tail
	maxN := min(len(list), 5)
	perItem := 2 // name row + preview row
	n := min(maxN, free/2)
	if n < min(maxN, 3) {
		perItem = 1
		n = min(maxN, max(0, free))
	}
	for i := 0; i < n; i++ {
		c := list[i]
		badge := ""
		if c.Unread > 0 {
			badge = " " + unreadLabel(c.Unread) + " "
		}
		bw := runewidth.StringWidth(badge)
		tm := listTime(c.LastTS)
		tw := runewidth.StringWidth(tm)
		right := bw + tw
		if bw > 0 && tw > 0 {
			right++
		}
		nm := trunc(clean(c.Title()), max(1, cw-3-right-1))
		gap := max(1, cw-3-runewidth.StringWidth(nm)-right)
		segs := []seg{
			{t: " ▸ ", fg: th.accent},
			{t: nm, fg: th.text, bold: c.Unread > 0},
			{t: spaces(gap)},
		}
		if bw > 0 {
			segs = append(segs, seg{t: badge, fg: th.bg, bg: th.accent, bold: true})
		}
		if bw > 0 && tw > 0 {
			segs = append(segs, seg{t: " "})
		}
		segs = append(segs, seg{t: tm, fg: th.dim})
		hit(0, cw, c.JID, "")
		add(segs...)
		if perItem == 2 {
			pv := strings.ReplaceAll(clean(prettyPreview(c.LastText)), "\n", " ")
			hit(0, cw, c.JID, "")
			add(seg{t: "   " + trunc(pv, max(1, cw-3)), fg: th.dim})
		}
	}

	// ---- quick buttons
	blank()
	type btn struct{ key, label, act string }
	btns := []btn{{"+", "New", "new"}, {"/", "Search", "search"}, {"t", "Theme", "theme"}}
	var bsegs []seg
	bx := 0
	for i, b := range btns {
		if i > 0 {
			bsegs = append(bsegs, seg{t: "  "})
			bx += 2
		}
		wd := runewidth.StringWidth(" " + b.key + " " + b.label + " ")
		bsegs = append(bsegs,
			seg{t: " " + b.key, fg: th.accent, bg: th.sel, bold: true},
			seg{t: " " + b.label + " ", fg: th.text, bg: th.sel})
		hit(bx, bx+wd, "", b.act)
		bx += wd
	}
	add(bsegs...)

	blank()
	text(th.dim, false, "c contacts · u unread · t theme · ? help")
	return rows
}

// dashClick handles a tap at pane column x, row y on the dashboard.
// The bool is false when nothing tappable was hit.
func (m *tuiModel) dashClick(x, y int) (tea.Model, tea.Cmd, bool) {
	for _, h := range m.dashHits {
		if h.y != y || x < h.x0 || x >= h.x1 {
			continue
		}
		if h.jid != "" {
			return m, m.openJIDByChat(h.jid), true
		}
		switch h.act {
		case "new":
			m.showDash = false
			return m, m.openContacts(), true
		case "search":
			m.showDash = false
			m.filtering = true
			m.filter.Focus()
			if !m.contactsOK {
				return m, tea.Batch(textinput.Blink, m.loadContacts()), true
			}
			return m, textinput.Blink, true
		case "theme":
			m.cycleTheme()
			return m, nil, true
		case "unread":
			m.showDash = false
			m.setTab(3)
			return m, nil, true
		case "pinned":
			m.showDash = false
			m.setTab(0)
			return m, nil, true
		case "drafts":
			if jid := m.firstDraft(); jid != "" {
				return m, m.openJIDByChat(jid), true
			}
			return m, nil, true
		}
	}
	return m, nil, false
}

// firstDraft is the chat with unsent text that shows first in the chat list.
func (m *tuiModel) firstDraft() string {
	for _, c := range m.chats {
		if strings.TrimSpace(m.drafts[c.JID]) != "" {
			return c.JID
		}
	}
	var keys []string
	for j, d := range m.drafts {
		if strings.TrimSpace(d) != "" {
			keys = append(keys, j)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// cycleTheme switches to the next theme and saves it in config.json.
func (m *tuiModel) cycleTheme() {
	names := themeNames()
	next := names[0]
	for i, n := range names {
		if themes[n] == th {
			next = names[(i+1)%len(names)]
			break
		}
	}
	applyTheme(next)
	c := loadConfig()
	c.Theme = next
	_ = c.Save()
	m.restyle()
	m.status = "theme: " + next
}

// renderDashScreen is the full-screen dashboard (phones: tap the "● wacli" bar or press d).
func (m *tuiModel) renderDashScreen() string {
	w := m.w
	rows := []string{render(th.bar, w,
		seg{t: " ● wacli", fg: th.accent, bold: true},
		seg{t: " — home", fg: th.text, bold: true})}
	rows = append(rows, m.dashLines(w)...)
	for len(rows) < m.h-1 {
		rows = append(rows, blankRow(w))
	}
	if len(rows) > m.h-1 {
		rows = rows[:m.h-1]
	}
	rows = append(rows, bgr(w, seg{t: " tap or press any key to close", fg: th.dim, ital: true}))
	return strings.Join(rows, "\n")
}

// ================================================================ drafts

// draftText is what is currently typed in the open chat (multi-line paste included).
func (m *tuiModel) draftText() string {
	if m.editID != "" {
		return ""
	}
	t := strings.Join(append(append([]string{}, m.lines...), m.input.Value()), "\n")
	if strings.TrimSpace(t) == "" || (len(m.lines) == 0 && strings.HasPrefix(t, "/")) {
		return ""
	}
	return t
}

// draftFor is the unsent text of any chat (live text for the chat that is open).
func (m *tuiModel) draftFor(jid string) string {
	if jid == m.cur {
		return m.draftText()
	}
	return m.drafts[jid]
}

// syncDraft stores the open chat's unsent text in drafts.json (only when it changed).
func (m *tuiModel) syncDraft() {
	if m.cur == "" {
		return
	}
	t := m.draftText()
	old, had := m.drafts[m.cur]
	switch {
	case t == "" && !had:
		return
	case t == "":
		delete(m.drafts, m.cur)
	case t == old:
		return
	default:
		m.drafts[m.cur] = t
	}
	_ = saveDrafts(m.drafts)
}

func (m *tuiModel) setDraft(d string) {
	parts := strings.Split(d, "\n")
	m.lines = append([]string{}, parts[:len(parts)-1]...)
	m.input.SetValue(parts[len(parts)-1])
	m.input.CursorEnd()
}

// onPaste keeps a multi-line paste as ONE message: the finished lines go to m.lines and
// the last, unfinished line stays in the input box. Nothing is sent until Enter is pressed.
func (m *tuiModel) onPaste(k tea.KeyMsg) bool {
	text := strings.ReplaceAll(string(k.Runes), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "  ")
	if !strings.Contains(text, "\n") {
		return false // single line: the input box handles it
	}
	parts := strings.Split(text, "\n")
	m.lines = append(m.lines, m.input.Value()+parts[0])
	m.lines = append(m.lines, parts[1:len(parts)-1]...)
	m.input.SetValue(parts[len(parts)-1])
	m.input.CursorEnd()
	return true
}

// ================================================================ global search

func (m *tuiModel) searching() bool {
	return m.filtering && strings.TrimSpace(m.filter.Value()) != ""
}

func (m *tuiModel) chatTitle(jid string) string {
	for _, c := range m.chats {
		if c.JID == jid {
			return c.Title()
		}
	}
	for _, c := range m.contacts {
		if c.JID == jid {
			return contactTitle(c)
		}
	}
	return userPart(jid)
}

// snippet shortens a message around the first match so the match stays visible.
func snippet(text, q string) string {
	text = strings.Join(strings.Fields(clean(text)), " ")
	lt := strings.ToLower(text)
	if i := strings.Index(lt, strings.ToLower(q)); i > 0 {
		if at := len([]rune(lt[:i])); at > 24 {
			if r := []rune(text); at-15 < len(r) {
				return "…" + string(r[at-15:])
			}
		}
	}
	return text
}

// runSearch rebuilds the results right away and asks the backend for message hits.
func (m *tuiModel) runSearch() tea.Cmd {
	q := strings.TrimSpace(m.filter.Value())
	m.rebuildResults()
	if len([]rune(q)) < 2 {
		m.hits = nil
		return nil
	}
	be := m.be
	return func() tea.Msg {
		hits, err := be.Search(SearchArgs{Query: q, Limit: 30})
		if err != nil { // e.g. an older daemon without search: chats + contacts still work
			return searchMsg{q: q}
		}
		return searchMsg{q: q, hits: hits}
	}
}

// rebuildResults: a typed number first, then chats, contacts without history, then messages.
func (m *tuiModel) rebuildResults() {
	q := strings.TrimSpace(m.filter.Value())
	if q == "" {
		m.res, m.rsel = nil, 0
		return
	}
	qd := digitsOnly(q)
	type scored struct {
		r searchRes
		s int
	}
	inChats := map[string]bool{}
	var chats, contacts []scored
	for _, c := range m.chats {
		if chatKind(c.JID) < 0 {
			continue
		}
		inChats[c.JID] = true
		sc := fuzzyScore(q, c.Title())
		if sc == 0 && len(qd) >= 3 && strings.Contains(userPart(c.JID), qd) {
			sc = 50
		}
		if sc == 0 {
			continue
		}
		sub := strings.ReplaceAll(clean(prettyPreview(c.LastText)), "\n", " ")
		chats = append(chats, scored{searchRes{kind: 0, jid: c.JID, title: c.Title(), sub: sub, ts: c.LastTS}, sc})
	}
	byScore := func(l []scored) {
		sort.SliceStable(l, func(i, j int) bool { return l[i].s > l[j].s })
	}
	byScore(chats)
	numberKnown := false
	for _, c := range m.contacts {
		if c.Phone != "" && c.Phone == qd {
			numberKnown = true
		}
		if inChats[c.JID] {
			continue
		}
		sc := fuzzyScore(q, c.Name)
		if sc == 0 && len(qd) >= 3 && strings.Contains(c.Phone, qd) {
			sc = 50
		}
		if sc == 0 {
			continue
		}
		sub := ""
		if c.Phone != "" {
			sub = "+" + c.Phone
		}
		contacts = append(contacts, scored{searchRes{kind: 1, jid: c.JID, title: contactTitle(c), sub: sub}, sc})
	}
	byScore(contacts)

	out := make([]searchRes, 0, len(chats)+len(contacts)+len(m.hits)+1)
	if isPhoneLike(q) && !numberKnown && !inChats[qd+"@s.whatsapp.net"] {
		out = append(out, searchRes{kind: 1, jid: qd + "@s.whatsapp.net", title: "+" + qd, sub: "Message this number"})
	}
	for _, c := range chats {
		out = append(out, c.r)
	}
	for i, c := range contacts {
		if i == 30 {
			break
		}
		out = append(out, c.r)
	}
	lq := strings.ToLower(q)
	n := 0
	for _, h := range m.hits {
		if n == 30 {
			break
		}
		if chatKind(h.Chat) < 0 || !strings.Contains(strings.ToLower(h.Text), lq) {
			continue
		}
		who := "You"
		if !h.FromMe {
			who = userPart(h.Sender)
		}
		out = append(out, searchRes{kind: 2, jid: h.Chat, title: m.chatTitle(h.Chat),
			sub: who + ": " + snippet(h.Text, q), ts: h.TS, msgID: h.ID})
		n++
	}
	m.res = out
	m.rsel = clamp(m.rsel, 0, max(0, len(out)-1))
}

func (m *tuiModel) moveRes(d int) {
	if len(m.res) == 0 {
		return
	}
	m.rsel = clamp(m.rsel+d, 0, len(m.res)-1)
}

func (m *tuiModel) resGeom(h int) (perPage, top int) {
	perPage = max(1, (h-4)/2)
	if len(m.res) > perPage {
		top = clamp(m.rsel-perPage/2, 0, len(m.res)-perPage)
	}
	return
}

func (m *tuiModel) openRes() tea.Cmd {
	if m.rsel < 0 || m.rsel >= len(m.res) {
		return nil
	}
	r := m.res[m.rsel]
	cmd := m.openJID(r.jid, r.title)
	if r.kind == 2 {
		m.jumpID = r.msgID
	}
	return cmd
}

func (m *tuiModel) renderResults(rows []string, w, h int) []string {
	for i := 2; i < h-1; i++ { // rows beside the floating buttons are narrower
		rows[i] = blankRow(fabRowW(w, i, h-1))
	}
	rows[1] = render(th.bar, w, seg{t: trunc(fmt.Sprintf(" Results  %d", len(m.res)), w), fg: th.accent, bold: true})
	if len(m.res) == 0 {
		rows[2] = bgr(w, seg{t: trunc(" No matches", w), fg: th.dim})
		rows[3] = bgr(w, seg{t: trunc(" Try a name, a number or a word", w), fg: th.dim})
	}
	perPage, top := m.resGeom(h)
	for i := 0; i < perPage; i++ {
		idx := top + i
		if idx >= len(m.res) {
			break
		}
		r := 2 + i*2
		if r+1 >= h-1 {
			break
		}
		l1, l2 := m.resItem(m.res[idx], idx == m.rsel, w)
		if rw := fabRowW(w, r+1, h-1); rw != w {
			n1, n2 := m.resItem(m.res[idx], idx == m.rsel, rw)
			if fabRowW(w, r, h-1) != w {
				l1 = n1
			}
			l2 = n2
		}
		rows[r], rows[r+1] = l1, l2
	}
	rows[h-1] = hintRow(w, "⏎", "open", "Esc", "clear")
	overlayFabs(rows, w, h-1)
	return rows
}

func (m *tuiModel) resItem(r searchRes, selected bool, w int) (string, string) {
	bg := th.bg
	if selected {
		bg = th.sel
	}
	name := clean(r.title)
	av := runewidth.FillRight(runewidth.Truncate(initials(strings.TrimPrefix(name, "+")), 2, ""), 2)
	tm := listTime(r.ts)
	if r.kind == 1 {
		tm = "contact"
	}
	tmW := runewidth.StringWidth(tm)
	nm := trunc(name, max(1, w-5-tmW-1))
	gap := max(1, w-4-runewidth.StringWidth(nm)-tmW-1)
	l1 := render(bg, w,
		seg{t: " "},
		seg{t: av, fg: "#FFFFFF", bg: avatarBgs[hashIdx(r.jid, len(avatarBgs))], bold: true},
		seg{t: " "},
		seg{t: nm, fg: th.text, bold: true},
		seg{t: spaces(gap)},
		seg{t: tm, fg: th.dim},
	)
	prefix := ""
	if r.kind == 2 {
		prefix = "↳ "
	}
	sub := strings.ReplaceAll(clean(r.sub), "\n", " ")
	l2 := render(bg, w,
		seg{t: "    "},
		seg{t: prefix, fg: th.accent},
		seg{t: trunc(sub, max(1, w-5-runewidth.StringWidth(prefix))), fg: th.dim},
	)
	return l1, l2
}

// ================================================================ file browser (📁)

// fileItem is one row of the file browser: a file, a folder, or the ".." entry.
type fileItem struct {
	name, path string
	dir, up    bool
	size       int64
	mod        time.Time
}

// button rows (y) of the file-action screen and of the send-confirmation screen
var fActY = [3]int{6, 9, 12}
var fConfY = [2]int{7, 10}

func btnHit(y, base int) bool { return y == base || y == base+1 }

func humanSize(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f < k:
		return fmt.Sprintf("%d B", n)
	case f < k*k:
		return fmt.Sprintf("%.0f KB", f/k)
	case f < k*k*k:
		return fmt.Sprintf("%.1f MB", f/(k*k))
	}
	return fmt.Sprintf("%.1f GB", f/(k*k*k))
}

func fileIcon(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic":
		return "📷"
	case ".mp4", ".mkv", ".mov", ".3gp", ".webm":
		return "🎬"
	case ".mp3", ".m4a", ".ogg", ".opus", ".wav", ".aac":
		return "🎵"
	case ".apk", ".zip", ".rar", ".7z", ".tar", ".gz":
		return "📦"
	}
	return "📄"
}

func shortPath(p string) string {
	if h := homeDir(); h != "" && (p == h || strings.HasPrefix(p, h+"/")) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

// truncLeft keeps the END of s (the interesting part of a long path).
func truncLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && runewidth.StringWidth("…"+string(r)) > w {
		r = r[1:]
	}
	return "…" + string(r)
}

// filesStartDir: the download folder (or the nearest parent that exists).
func filesStartDir() string {
	d := expandHome(loadConfig().DownloadDir)
	for p := d; p != ""; {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return homeDir()
}

// openWithDefault hands a file to the phone's default app (Termux: termux-open).
func openWithDefault(path string) error {
	for _, bin := range []string{"termux-open", "xdg-open", "open"} {
		p, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		cmd := exec.Command(p, path)
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	return fmt.Errorf("no opener found — run: pkg install termux-tools")
}

func (m *tuiModel) openFiles() tea.Cmd {
	m.showFiles = true
	m.fStage = 0
	m.ffilter.SetValue("")
	m.ffilter.Blur()
	m.loadFileDir(filesStartDir(), "")
	return nil
}

func (m *tuiModel) closeFiles() {
	m.showFiles = false
	m.fStage = 0
	m.ffilter.SetValue("")
	m.ffilter.Blur()
}

// loadFileDir lists dir: folders first (A-Z), then files (newest first).
func (m *tuiModel) loadFileDir(dir, focus string) {
	m.fDir, m.fItems, m.fSel = dir, nil, 0
	if parent := filepath.Dir(dir); parent != dir {
		m.fItems = append(m.fItems, fileItem{name: "..", path: parent, dir: true, up: true})
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		m.status = "⚠ " + err.Error()
		if os.IsPermission(err) {
			m.status = "⚠ no permission — run: termux-setup-storage"
		}
		return
	}
	var dirs, files []fileItem
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		p := filepath.Join(dir, name)
		st, err := os.Stat(p) // follows symlinks
		if err != nil {
			continue
		}
		it := fileItem{name: name, path: p, dir: st.IsDir(), size: st.Size(), mod: st.ModTime()}
		if it.dir {
			dirs = append(dirs, it)
		} else {
			files = append(files, it)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].name) < strings.ToLower(dirs[j].name) })
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	m.fItems = append(m.fItems, dirs...)
	m.fItems = append(m.fItems, files...)
	if len(m.fItems) > 1 && m.fItems[0].up {
		m.fSel = 1 // start on the first real entry, not on ".."
	}
	if focus != "" {
		for i, it := range m.fItems {
			if it.path == focus {
				m.fSel = i
			}
		}
	}
}

func (m *tuiModel) fileGeom(h int) (perPage, top int) {
	perPage = max(1, (h-3)/2)
	if len(m.fItems) > perPage {
		top = clamp(m.fSel-perPage/2, 0, len(m.fItems)-perPage)
	}
	return
}

func (m *tuiModel) fileChatGeom(h int) (perPage, top int) {
	perPage = max(1, (h-3)/2)
	if len(m.fchats) > perPage {
		top = clamp(m.fcSel-perPage/2, 0, len(m.fchats)-perPage)
	}
	return
}

func (m *tuiModel) moveFSel(d int) { m.fSel = clamp(m.fSel+d, 0, len(m.fItems)-1) }
func (m *tuiModel) moveFC(d int)   { m.fcSel = clamp(m.fcSel+d, 0, len(m.fchats)-1) }

func (m *tuiModel) fileUp() {
	parent := filepath.Dir(m.fDir)
	if parent == m.fDir {
		return
	}
	m.loadFileDir(parent, m.fDir)
}

// activateFile: tap on a row. Folders open, files show the Open / Send screen.
func (m *tuiModel) activateFile() {
	if m.fSel < 0 || m.fSel >= len(m.fItems) {
		return
	}
	it := m.fItems[m.fSel]
	switch {
	case it.up:
		m.fileUp()
	case it.dir:
		m.loadFileDir(it.path, "")
	default:
		m.fFile = it
		m.fStage = 1
	}
}

func (m *tuiModel) doOpenFile() tea.Cmd {
	f := m.fFile
	m.fStage = 0
	if err := openWithDefault(f.path); err != nil {
		m.status = "⚠ " + err.Error()
		return nil
	}
	m.status = "↗ opening " + f.name
	return nil
}

// startPickChat: "Send" was pressed, show the chat list to choose where it goes.
func (m *tuiModel) startPickChat() tea.Cmd {
	m.fStage = 2
	m.fcSel = 0
	m.ffilter.SetValue("")
	m.ffilter.Focus()
	m.refilterFileChats()
	return textinput.Blink
}

func (m *tuiModel) refilterFileChats() {
	q := strings.TrimSpace(m.ffilter.Value())
	var pin, rest []Chat
	for _, c := range m.chats {
		if k := chatKind(c.JID); k < 0 || k == 2 { // no status / channels: you can't send there
			continue
		}
		if q != "" && fuzzyScore(q, c.Title()) == 0 {
			continue
		}
		if m.pinned[c.JID] {
			pin = append(pin, c)
		} else {
			rest = append(rest, c)
		}
	}
	m.fchats = append(pin, rest...)
	m.fcSel = clamp(m.fcSel, 0, len(m.fchats)-1)
}

// sendPicked sends the chosen file to the chosen chat and opens that chat.
func (m *tuiModel) sendPicked() tea.Cmd {
	c, f, be := m.fTarget, m.fFile, m.be
	m.closeFiles()
	open := m.openJIDByChat(c.JID)
	m.status = "⏳ sending " + f.name + "…"
	send := m.act(func() (string, error) {
		if _, err := be.SendFile(FileArgs{Chat: c.JID, Path: f.path}); err != nil {
			return "", err
		}
		return "✓ sent " + f.name, nil
	})
	return tea.Batch(open, send)
}

func (m *tuiModel) onFilesKey(k tea.KeyMsg, s string) (tea.Model, tea.Cmd) {
	switch m.fStage {
	case 1: // Open / Send / Cancel
		switch s {
		case "o", "enter":
			return m, m.doOpenFile()
		case "s":
			return m, m.startPickChat()
		case "esc", "q", "c", "left", "h", "backspace":
			m.fStage = 0
		}
		return m, nil
	case 2: // choose a chat
		switch s {
		case "esc":
			if m.ffilter.Value() != "" {
				m.ffilter.SetValue("")
				m.refilterFileChats()
			} else {
				m.ffilter.Blur()
				m.fStage = 1
			}
			return m, nil
		case "enter":
			if m.fcSel >= 0 && m.fcSel < len(m.fchats) {
				m.fTarget = m.fchats[m.fcSel]
				m.ffilter.Blur()
				m.fStage = 3
			}
			return m, nil
		case "up":
			m.moveFC(-1)
			return m, nil
		case "down":
			m.moveFC(1)
			return m, nil
		case "pgup":
			m.moveFC(-5)
			return m, nil
		case "pgdown":
			m.moveFC(5)
			return m, nil
		}
		before := m.ffilter.Value()
		var cmd tea.Cmd
		m.ffilter, cmd = m.ffilter.Update(k)
		if m.ffilter.Value() != before {
			m.fcSel = 0
		}
		m.refilterFileChats()
		return m, cmd
	case 3: // confirm
		switch s {
		case "enter", "y":
			return m, m.sendPicked()
		case "esc", "n", "q", "backspace":
			m.fStage = 2
			m.ffilter.Focus()
			return m, textinput.Blink
		}
		return m, nil
	}
	page := max(1, (m.h-3)/2)
	switch s { // file list
	case "esc", "q":
		m.closeFiles()
	case "up", "k":
		m.moveFSel(-1)
	case "down", "j":
		m.moveFSel(1)
	case "pgup", "ctrl+u":
		m.moveFSel(-page)
	case "pgdown", "ctrl+d":
		m.moveFSel(page)
	case "g":
		m.fSel = 0
	case "G":
		m.moveFSel(len(m.fItems))
	case "left", "h", "backspace":
		m.fileUp()
	case "enter", "right", "l":
		m.activateFile()
	}
	return m, nil
}

func (m *tuiModel) onFilesMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	h := m.h
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		d := 2
		if msg.Button == tea.MouseButtonWheelUp {
			d = -2
		}
		switch m.fStage {
		case 0:
			m.moveFSel(d)
		case 2:
			m.moveFC(d)
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	y := msg.Y
	switch m.fStage {
	case 0:
		switch {
		case y == 0:
			m.closeFiles() // tap the "‹ Files" bar
		case y >= 2 && y < h-1:
			perPage, top := m.fileGeom(h)
			slot := (y - 2) / 2
			if slot < perPage && top+slot < len(m.fItems) {
				m.fSel = top + slot
				m.activateFile()
			}
		}
	case 1:
		switch {
		case y == 0 || btnHit(y, fActY[2]):
			m.fStage = 0
		case btnHit(y, fActY[0]):
			return m, m.doOpenFile()
		case btnHit(y, fActY[1]):
			return m, m.startPickChat()
		}
	case 2:
		switch {
		case y == 1:
			m.ffilter.Blur()
			m.fStage = 1
		case y >= 2 && y < h-1:
			perPage, top := m.fileChatGeom(h)
			slot := (y - 2) / 2
			if slot < perPage && top+slot < len(m.fchats) {
				m.fcSel = top + slot
				m.fTarget = m.fchats[m.fcSel]
				m.ffilter.Blur()
				m.fStage = 3
			}
		}
	case 3:
		switch {
		case y == 0 || btnHit(y, fConfY[1]):
			m.fStage = 2
			m.ffilter.Focus()
			return m, textinput.Blink
		case btnHit(y, fConfY[0]):
			return m, m.sendPicked()
		}
	}
	return m, nil
}

// ---- drawing

func (m *tuiModel) renderFiles() string {
	w, h := m.w, m.h
	rows := make([]string, h)
	for i := range rows {
		rows[i] = blankRow(w)
	}
	switch m.fStage {
	case 1:
		m.drawFileActions(rows, w, h)
	case 2:
		m.drawFilePick(rows, w, h)
	case 3:
		m.drawFileConfirm(rows, w, h)
	default:
		m.drawFileList(rows, w, h)
	}
	return strings.Join(rows, "\n")
}

func (m *tuiModel) fileRow(it fileItem, selected bool, w int) (string, string) {
	bg := th.bg
	if selected {
		bg = th.sel
	}
	icon, sub := "📁", "folder"
	switch {
	case it.up:
		icon, sub = "↑ ", "parent folder"
	case !it.dir:
		icon = fileIcon(it.name)
		sub = humanSize(it.size) + " · " + fmtTime(it.mod.Unix())
	}
	l1 := render(bg, w,
		seg{t: " "},
		seg{t: icon},
		seg{t: " "},
		seg{t: trunc(clean(it.name), max(1, w-5)), fg: th.text, bold: it.dir},
	)
	l2 := render(bg, w,
		seg{t: "    "},
		seg{t: trunc(sub, max(1, w-5)), fg: th.dim},
	)
	return l1, l2
}

// statusOrHint: the bottom row, showing the latest message if there is one.
func (m *tuiModel) statusOrHint(w int, pairs ...string) string {
	if m.status != "" {
		fg := th.accent
		if strings.HasPrefix(m.status, "⚠") {
			fg = warnColor
		}
		return bgr(w, seg{t: " " + trunc(m.status, max(1, w-2)), fg: fg})
	}
	return hintRow(w, pairs...)
}

func (m *tuiModel) drawFileList(rows []string, w, h int) {
	rows[0] = render(th.bar, w, seg{t: " ‹ Files", fg: th.accent, bold: true})
	rows[1] = bgr(w, seg{t: " " + truncLeft(shortPath(m.fDir), max(1, w-2)), fg: th.dim})
	perPage, top := m.fileGeom(h)
	for i := 0; i < perPage; i++ {
		idx := top + i
		if idx >= len(m.fItems) {
			break
		}
		r := 2 + i*2
		if r+1 >= h-1 {
			break
		}
		rows[r], rows[r+1] = m.fileRow(m.fItems[idx], idx == m.fSel, w)
	}
	nReal := 0
	for _, it := range m.fItems {
		if !it.up {
			nReal++
		}
	}
	if nReal == 0 {
		if r := 2 + len(m.fItems)*2; r < h-1 {
			rows[r] = bgr(w, seg{t: " No files here", fg: th.dim})
		}
	}
	rows[h-1] = m.statusOrHint(w, "⏎", "open", "←", "back", "Esc", "close")
}

func (m *tuiModel) drawButton(rows []string, y, w int, label string, primary bool) {
	bg, fg := th.accent, th.bg
	if !primary {
		bg, fg = th.sel, th.text
	}
	inner := max(1, w-2)
	t := runewidth.FillRight(trunc("  "+label, inner), inner)
	if y >= 0 && y < len(rows) {
		rows[y] = render(th.bg, w, seg{t: " "}, seg{t: t, fg: fg, bg: bg, bold: true})
	}
	if y+1 >= 0 && y+1 < len(rows) {
		rows[y+1] = render(th.bg, w, seg{t: " "}, seg{t: spaces(inner), bg: bg})
	}
}

func (m *tuiModel) drawFileInfo(rows []string, w int, f fileItem) {
	rows[2] = bgr(w, seg{t: " " + fileIcon(f.name) + " "}, seg{t: trunc(clean(f.name), max(1, w-5)), fg: th.text, bold: true})
	rows[3] = bgr(w, seg{t: "    " + trunc(humanSize(f.size)+" · "+fmtTime(f.mod.Unix()), max(1, w-5)), fg: th.dim})
	rows[4] = bgr(w, seg{t: " " + truncLeft(shortPath(filepath.Dir(f.path)), max(1, w-2)), fg: th.dim})
}

func (m *tuiModel) drawFileActions(rows []string, w, h int) {
	f := m.fFile
	rows[0] = render(th.bar, w, seg{t: " ‹ File", fg: th.accent, bold: true})
	m.drawFileInfo(rows, w, f)
	m.drawButton(rows, fActY[0], w, "📂  Open", true)
	m.drawButton(rows, fActY[1], w, "📤  Send to a chat", true)
	m.drawButton(rows, fActY[2], w, "✕  Cancel", false)
	rows[h-1] = hintRow(w, "o", "open", "s", "send", "Esc", "back")
}

func (m *tuiModel) drawFilePick(rows []string, w, h int) {
	v := m.ffilter.View()
	if pad := w - lipgloss.Width(v); pad > 0 {
		v += blankRow(pad)
	}
	rows[0] = v
	rows[1] = render(th.bar, w, seg{t: trunc(" ‹ Send "+clean(m.fFile.name)+" to…", w), fg: th.accent, bold: true})
	if len(m.fchats) == 0 {
		rows[2] = bgr(w, seg{t: " No chats found", fg: th.dim})
	}
	perPage, top := m.fileChatGeom(h)
	for i := 0; i < perPage; i++ {
		idx := top + i
		if idx >= len(m.fchats) {
			break
		}
		r := 2 + i*2
		if r+1 >= h-1 {
			break
		}
		rows[r], rows[r+1] = m.listItem(m.fchats[idx], idx == m.fcSel, w)
	}
	rows[h-1] = hintRow(w, "⏎", "pick", "Esc", "back")
}

func (m *tuiModel) drawFileConfirm(rows []string, w, h int) {
	rows[0] = render(th.bar, w, seg{t: " ‹ Send this file?", fg: th.accent, bold: true})
	m.drawFileInfo(rows, w, m.fFile)
	rows[5] = bgr(w, seg{t: " → " + trunc(clean(m.fTarget.Title()), max(1, w-5)), fg: th.accent, bold: true})
	m.drawButton(rows, fConfY[0], w, "✓  Send", true)
	m.drawButton(rows, fConfY[1], w, "✕  Cancel", false)
	rows[h-1] = hintRow(w, "⏎", "send", "Esc", "back")
}
