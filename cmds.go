package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ---------------------------------------------------------------- helpers

type parsed struct {
	pos  []string
	kv   map[string]string
	json bool
}

// parseArgs splits args into positionals and flags. `valued` lists flags that take a value.
func parseArgs(args []string, valued ...string) parsed {
	p := parsed{kv: map[string]string{}}
	isVal := map[string]bool{}
	for _, v := range valued {
		isVal[v] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			p.pos = append(p.pos, args[i+1:]...)
			return p
		case a == "--json":
			p.json = true
		case isVal[a] && i+1 < len(args):
			p.kv[a] = args[i+1]
			i++
		default:
			p.pos = append(p.pos, a)
		}
	}
	return p
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func openBackend(connect bool) (Backend, error) {
	if daemonAlive() {
		return newRemote(), nil
	}
	pass, err := askPass()
	if err != nil {
		return nil, err
	}
	e, err := NewEngine(pass)
	if err != nil {
		return nil, err
	}
	if !e.LoggedIn() {
		e.Close()
		return nil, errNotLinked
	}
	if connect {
		if err := e.Connect(); err != nil {
			e.Close()
			return nil, err
		}
		if err := e.WaitConnected(25 * time.Second); err != nil {
			e.Close()
			return nil, err
		}
	}
	return e, nil
}

func withBackend(connect bool, f func(b Backend) error) error {
	b, err := openBackend(connect)
	if err != nil {
		return err
	}
	defer b.Close()
	return f(b)
}

func fmtMessage(m Message) string {
	who := m.SenderName
	if m.FromMe {
		who = "me"
	} else if who == "" {
		who = userPart(m.Sender)
	}
	tick := ""
	if m.FromMe {
		switch m.Status {
		case 0:
			tick = " ✓"
		case 1:
			tick = " ✓✓"
		default:
			tick = col("36", " ✓✓")
		}
	}
	body := m.Text
	switch {
	case m.Deleted:
		body = col("2", "🚫 message deleted")
	case m.Kind != "text":
		label := "[" + m.Kind + "]"
		if m.Filename != "" {
			label = "[" + m.Filename + "]"
		}
		body = strings.TrimSpace(label + " " + m.Text)
	}
	if m.Edited && !m.Deleted {
		body += col("2", " (edited)")
	}
	if m.Reactions != "" {
		body += "  " + m.Reactions
	}
	return fmt.Sprintf("%s %s%s: %s  %s", col("2", fmtTime(m.TS)), col("1", who), tick, body, col("2", "#"+short(m.ID)))
}

func presenceText(p PresenceInfo) string {
	switch {
	case !p.Known:
		return "unknown (no presence update yet — contact may hide it)"
	case p.Online:
		return "online"
	case p.LastSeen > 0:
		return "offline, last seen " + fmtTime(p.LastSeen)
	}
	return "offline"
}

// ---------------------------------------------------------------- commands

func cmdLogin(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: wacli login <phone number with country code, e.g. 94771234567>")
	}
	phone := digitsOnly(args[0])
	if len(phone) < 8 {
		return errors.New("that doesn't look like a phone number (use country code, digits only)")
	}
	if daemonAlive() {
		return errors.New("daemon is running — stop it first: wacli daemon stop")
	}
	pass, err := askPass()
	if err != nil {
		return err
	}
	e, err := NewEngine(pass)
	if err != nil {
		return err
	}
	defer e.Close()
	if e.LoggedIn() {
		fmt.Println("Already linked as", e.cli.Store.ID.User)
		return nil
	}
	err = e.PairWithPhone(phone, func(code string) {
		fmt.Printf("\n  Pairing code:  %s\n\n", col("1;32", code))
		fmt.Println("On your phone: WhatsApp → Settings → Linked devices → Link a device")
		fmt.Println("→ \"Link with phone number instead\" → type the code above.")
		fmt.Println("\nWaiting (up to 3 minutes)…")
	})
	if err != nil {
		return err
	}
	fmt.Println("✓ Linked! Syncing contacts for a few seconds…")
	time.Sleep(12 * time.Second)
	return nil
}

func cmdLogout() error {
	if daemonAlive() {
		return errors.New("daemon is running — stop it first: wacli daemon stop")
	}
	pass, err := askPass()
	if err != nil {
		return err
	}
	e, err := NewEngine(pass)
	if err != nil {
		return err
	}
	defer e.Close()
	if !e.LoggedIn() {
		fmt.Println("Not linked.")
		return nil
	}
	if err := e.Connect(); err == nil {
		_ = e.WaitConnected(15 * time.Second)
	}
	if err := e.Logout(); err != nil {
		return err
	}
	fmt.Println("✓ Logged out (this device was removed from your linked devices).")
	return nil
}

func cmdStatus(args []string) error {
	f := parseArgs(args)
	return withBackend(false, func(b Backend) error {
		s, err := b.Status()
		if err != nil {
			return err
		}
		if f.json {
			return printJSON(s)
		}
		fmt.Printf("mode:       %s\nlinked:     %v\nconnected:  %v\nphone:      %s\nname:       %s\n",
			s.Mode, s.LoggedIn, s.Connected, s.Phone, s.PushName)
		return nil
	})
}

func cmdChats(args []string) error {
	f := parseArgs(args)
	return withBackend(false, func(b Backend) error {
		cs, err := b.Chats()
		if err != nil {
			return err
		}
		if f.json {
			return printJSON(cs)
		}
		if len(cs) == 0 {
			fmt.Println("No chats yet — messages show up here once they arrive (run: wacli daemon start).")
		}
		for _, c := range cs {
			un := "   "
			if c.Unread > 0 {
				un = col("1;32", fmt.Sprintf("(%d)", c.Unread))
			}
			prev := strings.ReplaceAll(c.LastText, "\n", " ")
			if r := []rune(prev); len(r) > 40 {
				prev = string(r[:40]) + "…"
			}
			fmt.Printf("%s %-24.24s %s  %s\n", col("2", fmtTime(c.LastTS)), c.Title(), un, col("2", prev))
		}
		return nil
	})
}

func cmdContacts(args []string) error {
	f := parseArgs(args)
	return withBackend(false, func(b Backend) error {
		cs, err := b.Contacts()
		if err != nil {
			return err
		}
		if len(f.pos) > 0 {
			q := strings.Join(f.pos, " ")
			var out []Contact
			for _, c := range cs {
				if fuzzyScore(q, c.Name) > 0 || strings.Contains(c.Phone, digitsOnly(q)) && digitsOnly(q) != "" {
					out = append(out, c)
				}
			}
			cs = out
		}
		if f.json {
			return printJSON(cs)
		}
		for _, c := range cs {
			id := c.Phone
			if id == "" {
				id = c.JID
			}
			fmt.Printf("%-28.28s %s\n", c.Name, col("2", id))
		}
		return nil
	})
}

func cmdGroups(args []string) error {
	f := parseArgs(args)
	return withBackend(true, func(b Backend) error {
		gs, err := b.Groups()
		if err != nil {
			return err
		}
		if len(f.pos) > 0 {
			jid, err := b.Resolve(strings.Join(f.pos, " "))
			if err != nil {
				return err
			}
			for _, g := range gs {
				if g.JID == jid {
					if f.json {
						return printJSON(g)
					}
					fmt.Printf("%s  (%d members)\n%s\n\n", col("1", g.Name), len(g.Members), g.Topic)
					for _, m := range g.Members {
						adm := ""
						if m.Admin {
							adm = col("33", " admin")
						}
						fmt.Printf("  %-26.26s %s%s\n", firstNonEmpty(m.Name, "?"), col("2", userPart(m.JID)), adm)
					}
					return nil
				}
			}
			return errors.New("not a group you're in")
		}
		if f.json {
			return printJSON(gs)
		}
		for _, g := range gs {
			fmt.Printf("%-28.28s %3d members  %s\n", g.Name, len(g.Members), col("2", g.JID))
		}
		return nil
	})
}

func cmdMsgs(args []string) error {
	f := parseArgs(args, "-n")
	if len(f.pos) < 1 {
		return errors.New("usage: wacli msgs <chat> [-n 20] [--json]")
	}
	n := 20
	if v, err := strconv.Atoi(f.kv["-n"]); err == nil && v > 0 {
		n = v
	}
	return withBackend(false, func(b Backend) error {
		ms, err := b.Messages(MsgQuery{Chat: strings.Join(f.pos, " "), Limit: n})
		if err != nil {
			return err
		}
		if f.json {
			return printJSON(ms)
		}
		for _, m := range ms {
			fmt.Println(fmtMessage(m))
		}
		return nil
	})
}

func cmdSend(args []string) error {
	f := parseArgs(args, "--reply")
	if len(f.pos) < 2 {
		return errors.New("usage: wacli send <chat> <text...>  (use - as text to read stdin)  [--reply <msg-id>]")
	}
	text := strings.Join(f.pos[1:], " ")
	if text == "-" {
		b, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			return err
		}
		text = strings.TrimRight(string(b), "\n")
	}
	return withBackend(true, func(b Backend) error {
		r, err := b.Send(SendArgs{Chat: f.pos[0], Text: text, ReplyTo: f.kv["--reply"]})
		if err != nil {
			return err
		}
		if f.json {
			return printJSON(r)
		}
		fmt.Printf("✓ sent #%s\n", short(r.ID))
		return nil
	})
}

func cmdSendFile(args []string) error {
	f := parseArgs(args, "--reply")
	if len(f.pos) < 2 {
		return errors.New("usage: wacli sendfile <chat> <path> [caption...] [--reply <msg-id>]")
	}
	path, err := filepath.Abs(expandHome(f.pos[1]))
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return withBackend(true, func(b Backend) error {
		r, err := b.SendFile(FileArgs{
			Chat: f.pos[0], Path: path,
			Caption: strings.Join(f.pos[2:], " "), ReplyTo: f.kv["--reply"],
		})
		if err != nil {
			return err
		}
		if f.json {
			return printJSON(r)
		}
		fmt.Printf("✓ sent #%s\n", short(r.ID))
		return nil
	})
}

func cmdGet(args []string) error {
	f := parseArgs(args, "--dir")
	if len(f.pos) < 2 {
		return errors.New("usage: wacli get <chat> <msg-id> [--dir <folder>]")
	}
	dir := f.kv["--dir"]
	if dir != "" {
		var err error
		if dir, err = filepath.Abs(expandHome(dir)); err != nil {
			return err
		}
	}
	return withBackend(true, func(b Backend) error {
		p, err := b.Download(DlArgs{Chat: f.pos[0], ID: f.pos[1], Dir: dir})
		if err != nil {
			return err
		}
		if f.json {
			return printJSON(map[string]string{"path": p})
		}
		fmt.Println("✓ saved:", p)
		return nil
	})
}

func cmdReact(args []string) error {
	f := parseArgs(args)
	if len(f.pos) < 3 {
		return errors.New("usage: wacli react <chat> <msg-id> <emoji>   (empty \"\" removes your reaction)")
	}
	return withBackend(true, func(b Backend) error {
		if err := b.React(ReactArgs{Chat: f.pos[0], ID: f.pos[1], Emoji: f.pos[2]}); err != nil {
			return err
		}
		fmt.Println("✓ reacted")
		return nil
	})
}

func cmdEdit(args []string) error {
	f := parseArgs(args)
	if len(f.pos) < 3 {
		return errors.New("usage: wacli edit <chat> <msg-id> <new text...>")
	}
	return withBackend(true, func(b Backend) error {
		if err := b.Edit(EditArgs{Chat: f.pos[0], ID: f.pos[1], Text: strings.Join(f.pos[2:], " ")}); err != nil {
			return err
		}
		fmt.Println("✓ edited")
		return nil
	})
}

func cmdRm(args []string) error {
	f := parseArgs(args)
	if len(f.pos) < 2 {
		return errors.New("usage: wacli rm <chat> <msg-id>   (delete for everyone, own messages only)")
	}
	return withBackend(true, func(b Backend) error {
		if err := b.Delete(MsgRef{Chat: f.pos[0], ID: f.pos[1]}); err != nil {
			return err
		}
		fmt.Println("✓ deleted")
		return nil
	})
}

func cmdRead(args []string) error {
	f := parseArgs(args)
	if len(f.pos) < 1 {
		return errors.New("usage: wacli read <chat>   (mark chat as read, sends blue ticks)")
	}
	return withBackend(true, func(b Backend) error {
		if err := b.MarkRead(strings.Join(f.pos, " ")); err != nil {
			return err
		}
		fmt.Println("✓ marked as read")
		return nil
	})
}

func cmdPresence(args []string) error {
	f := parseArgs(args)
	if len(f.pos) < 1 {
		return errors.New("usage: wacli presence <chat>")
	}
	return withBackend(true, func(b Backend) error {
		chat := strings.Join(f.pos, " ")
		jid, err := b.Resolve(chat)
		if err != nil {
			return err
		}
		ch, cancel, err := b.Subscribe()
		if err != nil {
			return err
		}
		defer cancel()
		p, err := b.Presence(jid)
		if err != nil {
			return err
		}
		if !p.Known {
			timeout := time.After(5 * time.Second)
		wait:
			for {
				select {
				case ev, ok := <-ch:
					if !ok {
						break wait
					}
					if ev.Type == "presence" && ev.Chat == jid {
						break wait
					}
				case <-timeout:
					break wait
				}
			}
			p, _ = b.Presence(jid)
		}
		if f.json {
			return printJSON(p)
		}
		fmt.Println(presenceText(p))
		return nil
	})
}

func cmdWatch(args []string) error {
	f := parseArgs(args)
	return withBackend(true, func(b Backend) error {
		filter := ""
		if len(f.pos) > 0 {
			j, err := b.Resolve(strings.Join(f.pos, " "))
			if err != nil {
				return err
			}
			filter = j
		}
		ch, cancel, err := b.Subscribe()
		if err != nil {
			return err
		}
		defer cancel()
		titles := map[string]string{}
		title := func(jid string) string {
			if t, ok := titles[jid]; ok {
				return t
			}
			if cs, err := b.Chats(); err == nil {
				for _, c := range cs {
					titles[c.JID] = c.Title()
				}
			}
			if t, ok := titles[jid]; ok {
				return t
			}
			return userPart(jid)
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		fmt.Fprintln(os.Stderr, "watching… Ctrl+C to stop")
		for {
			select {
			case <-sig:
				return nil
			case ev, ok := <-ch:
				if !ok {
					return nil
				}
				if filter != "" && ev.Chat != filter {
					continue
				}
				if f.json {
					_ = json.NewEncoder(os.Stdout).Encode(ev)
					continue
				}
				switch ev.Type {
				case "message":
					if ev.Msg != nil {
						fmt.Printf("%s %s\n", col("35", "["+title(ev.Chat)+"]"), fmtMessage(*ev.Msg))
					}
				case "presence":
					state := "offline"
					if ev.On {
						state = "online"
					}
					fmt.Printf("%s %s is %s\n", col("2", time.Now().Format("15:04")), title(ev.Chat), state)
				case "typing":
					if ev.On {
						fmt.Printf("%s %s is typing…\n", col("2", time.Now().Format("15:04")), title(ev.Chat))
					}
				}
			}
		}
	})
}

func cmdAlias(args []string) error {
	cfg := loadConfig()
	if len(args) == 0 || args[0] == "list" {
		keys := make([]string, 0, len(cfg.Aliases))
		for k := range cfg.Aliases {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%-16s → %s\n", k, cfg.Aliases[k])
		}
		return nil
	}
	switch args[0] {
	case "set":
		if len(args) < 3 {
			return errors.New("usage: wacli alias set <name> <phone | jid | contact name>")
		}
		cfg.Aliases[args[1]] = strings.Join(args[2:], " ")
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("✓ %s → %s\n", args[1], cfg.Aliases[args[1]])
	case "rm":
		if len(args) < 2 {
			return errors.New("usage: wacli alias rm <name>")
		}
		delete(cfg.Aliases, args[1])
		return cfg.Save()
	default:
		return errors.New("usage: wacli alias [list | set <name> <target> | rm <name>]")
	}
	return nil
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "on", "true", "yes", "1":
		return true, nil
	case "off", "false", "no", "0":
		return false, nil
	}
	return false, fmt.Errorf("expected on/off, got %q", s)
}

func cmdConfig(args []string) error {
	cfg := loadConfig()
	if len(args) == 0 {
		return printJSON(cfg)
	}
	if args[0] != "set" || len(args) < 3 {
		return errors.New("usage: wacli config [set <notifications|send_read_receipts|send_typing|download_dir> <value>]")
	}
	val := strings.Join(args[2:], " ")
	var err error
	switch args[1] {
	case "notifications":
		cfg.Notifications, err = parseBool(val)
	case "send_read_receipts":
		cfg.SendReceipts, err = parseBool(val)
	case "send_typing":
		cfg.SendTyping, err = parseBool(val)
	case "download_dir":
		cfg.DownloadDir = expandHome(val)
	default:
		return fmt.Errorf("unknown setting %q", args[1])
	}
	if err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println("✓ saved (a running daemon picks this up automatically)")
	return nil
}

// ---------------------------------------------------------------- daemon

func cmdDaemon(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "start":
		return daemonStart()
	case "stop":
		return daemonStop()
	case "status":
		if daemonAlive() {
			s, err := newRemote().Status()
			if err != nil {
				return err
			}
			fmt.Printf("daemon running · linked=%v connected=%v phone=%s\n", s.LoggedIn, s.Connected, s.Phone)
		} else {
			fmt.Println("daemon not running  (start it: wacli daemon start)")
		}
		return nil
	case "run": // foreground, handy for debugging
		pass, err := askPass()
		if err != nil {
			return err
		}
		return runDaemon(pass)
	case "run-child": // internal: passphrase arrives on stdin
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		return runDaemon(strings.TrimRight(line, "\r\n"))
	}
	return errors.New("usage: wacli daemon [start|stop|status|run]")
}

func daemonStart() error {
	if daemonAlive() {
		fmt.Println("daemon already running")
		return nil
	}
	pass, err := askPass()
	if err != nil {
		return err
	}
	if err := CheckPass(pass); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(dataDir(), "daemon.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon", "run-child")
	cmd.Stdin = strings.NewReader(pass + "\n")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	for i := 0; i < 60; i++ {
		if daemonAlive() {
			fmt.Println("✓ daemon started (unlocked for this session). Stop it with: wacli daemon stop")
			if p, err := exec.LookPath("termux-wake-lock"); err == nil {
				_ = exec.Command(p).Run()
				fmt.Println("  (termux wake-lock enabled so Android doesn't kill it)")
			}
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("daemon didn't come up — see %s", logPath)
}

func daemonStop() error {
	if !daemonAlive() {
		fmt.Println("daemon not running")
		return nil
	}
	if err := newRemote().do("shutdown", nil, nil); err != nil {
		return err
	}
	for i := 0; i < 40; i++ {
		if !daemonAlive() {
			fmt.Println("✓ daemon stopped, database re-encrypted")
			if p, err := exec.LookPath("termux-wake-unlock"); err == nil {
				_ = exec.Command(p).Run()
			}
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("daemon is taking a long time to stop")
}

func runDaemon(pass string) error {
	e, err := NewEngine(pass)
	if err != nil {
		return err
	}
	if !e.LoggedIn() {
		e.Close()
		return errNotLinked
	}
	if err := e.Connect(); err != nil {
		e.Close()
		return err
	}
	shutdown := make(chan struct{}, 1)
	ln, err := e.Serve(shutdown)
	if err != nil {
		e.Close()
		return err
	}
	fmt.Println(time.Now().Format(time.RFC3339), "daemon running")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-shutdown:
	}
	ln.Close()
	_ = os.Remove(sockPath())
	e.Close()
	fmt.Println(time.Now().Format(time.RFC3339), "daemon stopped")
	return nil
}
