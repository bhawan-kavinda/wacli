package main

import (
	"fmt"
	"hash/fnv"
	"os"
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

func tabName(k int) string { return [...]string{"Chats", "Groups", "Channels"}[k] }

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
type errMsg struct{ err error }
type evMsg Event
type evClosedMsg struct{}
type tickMsg time.Time

type tuiModel struct {
	be     Backend
	w, h   int
	online bool

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
}

func runTUI(b Backend) error {
	if os.Getenv("WACLI_256") == "" {
		lipgloss.SetColorProfile(termenv.TrueColor) // Termux supports 24-bit colour
	}
	applyTheme(loadConfig().Theme)
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = "Type a message…  (/help)"
	ti.CharLimit = 4000
	fi := textinput.New()
	fi.Prompt = "🔍 "
	fi.Placeholder = "search"

	m := &tuiModel{be: b, input: ti, filter: fi, selMsg: -1}
	m.restyle()
	if ch, cancel, err := b.Subscribe(); err == nil {
		m.events, m.cancel = ch, cancel
	} else {
		m.status = "⚠ live updates unavailable: " + err.Error()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
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
	case chatsMsg:
		m.chats = []Chat(msg)
		m.refilter()
	case statusMsg:
		m.online = msg.Connected
	case msgsMsg:
		if msg.chat == m.cur {
			m.msgs = msg.msgs
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
		if m.typing && time.Now().After(m.typingUntil) {
			m.typing = false
		}
		return m, tickCmd()
	case evClosedMsg:
		m.status = "⚠ lost connection to the daemon"
	case evMsg:
		return m.onEvent(Event(msg))
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m *tuiModel) onEvent(ev Event) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{m.waitEvent()}
	switch ev.Type {
	case "message", "update":
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
		if ev.Chat == m.cur {
			m.typing = ev.On
			m.typingUntil = time.Now().Add(7 * time.Second)
		}
	case "connected":
		m.online = true
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
	return list
}

func (m *tuiModel) tabUnread(kind int) int {
	n := 0
	for _, c := range m.chats {
		if chatKind(c.JID) == kind {
			n += c.Unread
		}
	}
	return n
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
		if chatKind(c.JID) != m.tab {
			continue
		}
		if m.filtering && q != "" && fuzzyScore(q, c.Title()) == 0 {
			continue
		}
		out = append(out, c)
	}
	m.shown = out
	m.fixSel()
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
	m.cur, m.curName = c.JID, c.Title()
	m.msgs, m.scroll, m.pres, m.typing = nil, 0, PresenceInfo{}, false
	m.resetChatState()
	m.filtering = false
	m.filter.SetValue("")
	m.filter.Blur()
	m.refilter()
	m.input.SetValue("")
	m.inChat = true
	m.input.Focus()
	be := m.be
	jid := c.JID
	return tea.Batch(
		textinput.Blink,
		m.loadMsgs(jid),
		m.markRead(jid),
		m.loadPresence(jid),
		func() tea.Msg { _ = be.SetActive(jid); return nil },
	)
}

func (m *tuiModel) leaveChat() tea.Cmd {
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
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	if !m.inChat {
		return m.onListKey(k, s)
	}
	return m.onChatKey(k, s)
}

func (m *tuiModel) onListKey(k tea.KeyMsg, s string) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch s {
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.refilter()
			return m, nil
		case "enter":
			return m, m.openSel()
		case "up":
			m.moveSel(-1)
			return m, nil
		case "down":
			m.moveSel(1)
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(k)
		m.refilter()
		return m, cmd
	}
	switch s {
	case "q":
		return m, m.quit()
	case "?":
		m.showHelp = true
	case "/":
		m.filtering = true
		m.filter.Focus()
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
		m.inChat = false
		m.input.Blur()
		return m, nil
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
	text := strings.TrimSpace(m.input.Value())
	if text == "" || m.cur == "" {
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
	if strings.HasPrefix(text, "/") {
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
	line("← → 1 2", "Chats / Groups tabs")
	line("j k  ↑ ↓", "move")
	line("/", "search this tab")
	line("q", "quit")
	blank()
	title("In a chat")
	line("⏎", "send message")
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

func (m *tuiModel) tabBar(w int) string {
	segs := []seg{{t: " "}}
	used := 1
	for _, k := range m.tabList() {
		label := " " + tabName(k)
		if n := m.tabUnread(k); n > 0 {
			label += fmt.Sprintf(" %d", n)
		}
		label += " "
		lw := runewidth.StringWidth(label)
		if used+lw+1 > w {
			break
		}
		if k == m.tab {
			segs = append(segs, seg{t: label, fg: th.bg, bg: th.accent, bold: true})
		} else {
			segs = append(segs, seg{t: label, fg: th.dim})
		}
		segs = append(segs, seg{t: " "})
		used += lw + 1
	}
	return bgr(w, segs...)
}

func (m *tuiModel) renderList(w, h int) []string {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = blankRow(w)
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
			seg{t: " wacli", fg: th.text, bold: true})
	}
	rows[1] = m.tabBar(w)

	if len(m.shown) == 0 {
		rows[2] = bgr(w, seg{t: trunc(" Nothing here yet", w), fg: th.dim})
		rows[3] = bgr(w, seg{t: trunc(" New messages show up here", w), fg: th.dim})
	}
	perPage := max(1, (h-3)/2)
	top := 0
	if len(m.shown) > perPage {
		top = clamp(m.sel-perPage/2, 0, len(m.shown)-perPage)
	}
	for i := 0; i < perPage; i++ {
		idx := top + i
		if idx >= len(m.shown) {
			break
		}
		r := 2 + i*2
		if r+1 >= h-1 {
			break
		}
		rows[r], rows[r+1] = m.listItem(m.shown[idx], idx == m.sel, w)
	}
	if m.inChat {
		rows[h-1] = hintRow(w, "Tab", "back to list")
	} else {
		rows[h-1] = hintRow(w, "⏎", "open", "←→", "tabs", "/", "find", "?", "help", "q", "quit")
	}
	return rows
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
	nm := trunc(name, max(1, w-5-tmW-1))
	gap := max(1, w-4-runewidth.StringWidth(nm)-tmW-1)
	l1 := render(bg, w,
		seg{t: " "},
		seg{t: av, fg: "#FFFFFF", bg: avatarBgs[hashIdx(c.JID, len(avatarBgs))], bold: true},
		seg{t: " "},
		seg{t: nm, fg: nameFg, bold: c.Unread > 0},
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
	pv := trunc(strings.ReplaceAll(clean(prettyPreview(c.LastText)), "\n", " "), max(1, w-4-bw-1))
	gap2 := max(0, w-4-runewidth.StringWidth(pv)-bw-1)
	l2 := render(bg, w,
		seg{t: "    "},
		seg{t: pv, fg: th.dim},
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
		rows[1] = bgr(w, seg{t: trunc(" Pick a chat from the list", w), fg: th.dim})
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

// bubble draws one WhatsApp-style message: rounded corners (quadrant/half blocks),
// own messages on the right, a sharp "tail" corner on the first message of a run.
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
	tl, tr := "▗", "▖"
	if tail {
		if mm.FromMe {
			tr = "▄"
		} else {
			tl = "▄"
		}
	}
	edge := func(l, mid, r string) []seg {
		return []seg{
			{t: l, fg: bb, bg: th.bg},
			{t: strings.Repeat(mid, total-2), fg: bb, bg: th.bg},
			{t: r, fg: bb, bg: th.bg},
		}
	}
	cores := [][]seg{edge(tl, "▄", tr)}
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
	cores = append(cores, edge("▝", "▀", "▘"))

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
