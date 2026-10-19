package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

var (
	stTitle  = lipgloss.NewStyle().Bold(true)
	stDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	stMe     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	stSel    = lipgloss.NewStyle().Reverse(true)
	stUnread = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	stWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

const helpText = "/r N text · /react N 👍 · /edit N text · /del N · /get N · /file path [caption] · Esc=chats · ✓ sent ✓✓ delivered ✔✔ read"

// ---- tea messages

type chatsMsg []Chat
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
	chats  []Chat
	sel    int
	selJID string

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

	status string
	events <-chan Event
	cancel func()
}

func runTUI(b Backend) error {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.CharLimit = 4000
	m := &tuiModel{be: b, input: ti}
	if ch, cancel, err := b.Subscribe(); err == nil {
		m.events, m.cancel = ch, cancel
	} else {
		m.status = "⚠ live updates unavailable: " + err.Error()
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// ---------------------------------------------------------------- commands (async)

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
	case chatsMsg:
		m.chats = []Chat(msg)
		m.fixSel()
	case msgsMsg:
		if msg.chat == m.cur {
			m.msgs = msg.msgs
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
		m.status = "connected"
	case "disconnected":
		m.status = "⚠ disconnected — reconnecting…"
	}
	return m, tea.Batch(cmds...)
}

func (m *tuiModel) fixSel() {
	if len(m.chats) == 0 {
		m.sel = 0
		return
	}
	if m.selJID != "" {
		for i, c := range m.chats {
			if c.JID == m.selJID {
				m.sel = i
				return
			}
		}
	}
	m.sel = clamp(m.sel, 0, len(m.chats)-1)
	m.selJID = m.chats[m.sel].JID
}

func (m *tuiModel) moveSel(d int) {
	if len(m.chats) == 0 {
		return
	}
	m.sel = clamp(m.sel+d, 0, len(m.chats)-1)
	m.selJID = m.chats[m.sel].JID
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

func (m *tuiModel) openSel() tea.Cmd {
	if len(m.chats) == 0 {
		return nil
	}
	c := m.chats[m.sel]
	m.cur, m.curName = c.JID, c.Title()
	m.msgs, m.scroll, m.pres, m.typing = nil, 0, PresenceInfo{}, false
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
	be := m.be
	return func() tea.Msg {
		_ = be.Typing(TypingArgs{Chat: chat, On: false})
		_ = be.SetActive("")
		return nil
	}
}

func (m *tuiModel) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, m.quit()
	}
	if !m.inChat { // chat list has focus
		switch s {
		case "q":
			return m, m.quit()
		case "up", "k":
			m.moveSel(-1)
		case "down", "j":
			m.moveSel(1)
		case "g":
			m.moveSel(-len(m.chats))
		case "G":
			m.moveSel(len(m.chats))
		case "enter", "right", "l":
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

	page := max(3, m.h-5)
	switch s {
	case "esc":
		return m, m.leaveChat()
	case "tab":
		m.inChat = false
		m.input.Blur()
		return m, nil
	case "pgup", "ctrl+u":
		m.scroll += page
		return m, nil
	case "pgdown", "ctrl+d":
		m.scroll = max(0, m.scroll-page)
		return m, nil
	case "up":
		m.scroll += 3
		return m, nil
	case "down":
		m.scroll = max(0, m.scroll-3)
		return m, nil
	case "enter":
		return m, m.submit()
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	cmds := []tea.Cmd{cmd}
	v := m.input.Value()
	if v != before && v != "" && !strings.HasPrefix(v, "/") && time.Since(m.lastTyping) > 4*time.Second {
		m.lastTyping = time.Now()
		cmds = append(cmds, m.typingCmd(m.cur, true))
	}
	return m, tea.Batch(cmds...)
}

func (m *tuiModel) pick(rest string) (Message, string, error) {
	f := strings.Fields(rest)
	if len(f) == 0 {
		return Message{}, "", fmt.Errorf("give a message number, e.g. #3")
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
	m.input.SetValue("")
	m.scroll = 0
	m.status = ""
	chat, be := m.cur, m.be

	if !strings.HasPrefix(text, "/") {
		return tea.Batch(
			m.typingCmd(chat, false),
			m.act(func() (string, error) {
				_, err := be.Send(SendArgs{Chat: chat, Text: text})
				return "", err
			}),
		)
	}
	fields := strings.Fields(text)
	cmd := strings.ToLower(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	fail := func(err error) tea.Cmd {
		m.status = "⚠ " + err.Error()
		return nil
	}
	switch cmd {
	case "/help", "/?":
		m.status = helpText
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
		return m.act(func() (string, error) {
			return "", be.React(ReactArgs{Chat: chat, ID: mm.ID, Emoji: body})
		})
	case "/edit":
		mm, body, err := m.pick(rest)
		if err != nil || body == "" {
			return fail(fmt.Errorf("usage: /edit N new text"))
		}
		return m.act(func() (string, error) {
			return "", be.Edit(EditArgs{Chat: chat, ID: mm.ID, Text: body})
		})
	case "/del", "/rm":
		mm, _, err := m.pick(rest)
		if err != nil {
			return fail(err)
		}
		return m.act(func() (string, error) {
			return "", be.Delete(MsgRef{Chat: chat, ID: mm.ID})
		})
	case "/get":
		mm, _, err := m.pick(rest)
		if err != nil {
			return fail(err)
		}
		return m.act(func() (string, error) {
			p, err := be.Download(DlArgs{Chat: chat, ID: mm.ID})
			if err != nil {
				return "", err
			}
			return "saved: " + p, nil
		})
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
	default:
		m.status = "⚠ unknown command " + cmd + " (try /help)"
	}
	return nil
}

// ---------------------------------------------------------------- view

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.FillRight(runewidth.Truncate(s, w, "…"), w)
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

func presStr(p PresenceInfo, typing bool) string {
	switch {
	case typing:
		return "typing…"
	case !p.Known:
		return ""
	case p.Online:
		return "online"
	case p.LastSeen > 0:
		return "last seen " + fmtTime(p.LastSeen)
	}
	return "offline"
}

// paneWidths returns {left, right}; one of them is 0 on narrow screens.
func (m *tuiModel) paneWidths() [2]int {
	if m.w < 70 {
		if m.inChat {
			return [2]int{0, m.w}
		}
		return [2]int{m.w, 0}
	}
	l := clamp(m.w/3, 22, 34)
	return [2]int{l, m.w - l - 1}
}

func (m *tuiModel) View() string {
	if m.w == 0 || m.h == 0 {
		return "loading…"
	}
	if m.h < 6 {
		return "terminal too small"
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
	sep := stDim.Render("│")
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

func (m *tuiModel) renderList(w, h int) []string {
	rows := make([]string, h)
	rows[0] = stTitle.Render(fit(" Chats", w))
	visible := h - 2
	top := 0
	if len(m.chats) > visible {
		top = clamp(m.sel-visible/2, 0, len(m.chats)-visible)
	}
	for i := 0; i < visible; i++ {
		idx := top + i
		if idx >= len(m.chats) {
			rows[1+i] = strings.Repeat(" ", w)
			continue
		}
		c := m.chats[idx]
		mark := "  "
		if idx == m.sel {
			mark = "▸ "
		}
		label := mark + c.Title()
		if c.Unread > 0 {
			label += fmt.Sprintf(" (%d)", c.Unread)
		}
		line := fit(label, w)
		switch {
		case idx == m.sel && !m.inChat:
			line = stSel.Render(line)
		case idx == m.sel:
			line = stTitle.Render(line)
		case c.Unread > 0:
			line = stUnread.Render(line)
		}
		rows[1+i] = line
	}
	hint := " q quit · ⏎ open · j/k move"
	if m.inChat {
		hint = " Tab: back to list"
	}
	rows[h-1] = stDim.Render(fit(hint, w))
	return rows
}

func (m *tuiModel) msgLines(w int) []string {
	byID := make(map[string]Message, len(m.msgs))
	for _, mm := range m.msgs {
		byID[mm.ID] = mm
	}
	var out []string
	for i, mm := range m.msgs {
		who := mm.SenderName
		if mm.FromMe {
			who = "me"
		} else if who == "" {
			who = userPart(mm.Sender)
		}
		tick := ""
		if mm.FromMe {
			tick = [...]string{" ✓", " ✓✓", " ✔✔"}[clamp(mm.Status, 0, 2)]
		}
		body := mm.Text
		switch {
		case mm.Deleted:
			body = "🚫 message deleted"
		case mm.Kind != "text":
			label := "[" + mm.Kind + "]"
			if mm.Filename != "" {
				label = "[" + mm.Filename + "]"
			}
			body = strings.TrimSpace(label + " " + mm.Text)
		}
		if mm.Edited && !mm.Deleted {
			body += " (edited)"
		}
		if mm.Reactions != "" {
			body += "  " + mm.Reactions
		}
		if mm.ReplyTo != "" {
			q := "↪ (earlier message)"
			if qm, ok := byID[mm.ReplyTo]; ok {
				qw := qm.SenderName
				if qm.FromMe {
					qw = "me"
				} else if qw == "" {
					qw = userPart(qm.Sender)
				}
				qt := strings.ReplaceAll(previewOf(&qm), "\n", " ")
				q = "↪ " + qw + ": " + runewidth.Truncate(qt, 40, "…")
			}
			for _, l := range wrapText("    "+q, w) {
				out = append(out, stDim.Render(l))
			}
		}
		line := fmt.Sprintf("%d %s %s%s: %s", i+1, time.Unix(mm.TS, 0).Format("15:04"), who, tick, body)
		for _, l := range wrapText(line, w) {
			if mm.FromMe {
				out = append(out, stMe.Render(l))
			} else {
				out = append(out, l)
			}
		}
	}
	return out
}

func (m *tuiModel) renderChat(w, h int) []string {
	rows := make([]string, h)
	title := "No chat open"
	if m.cur != "" {
		title = m.curName
		if ps := presStr(m.pres, m.typing); ps != "" {
			title += " · " + ps
		}
	}
	rows[0] = stTitle.Render(runewidth.Truncate(" "+title, w, "…"))

	avail := h - 3
	if m.cur == "" {
		rows[1] = stDim.Render(" Select a chat with ↑/↓ and Enter")
	} else {
		lines := m.msgLines(w - 1)
		maxScroll := max(0, len(lines)-avail)
		m.scroll = clamp(m.scroll, 0, maxScroll)
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

	st := m.status
	style := stDim
	if st == "" && m.cur != "" {
		st = "/help for commands"
	}
	if strings.HasPrefix(st, "⚠") {
		style = stWarn
	}
	rows[h-2] = style.Render(runewidth.Truncate(st, w, "…"))
	if m.cur != "" {
		rows[h-1] = m.input.View()
	}
	return rows
}
