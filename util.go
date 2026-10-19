package main

import (
	"bufio"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"golang.org/x/term"
)

var errNotLinked = errors.New("not linked to WhatsApp yet — run: wacli login <phone number with country code>")

var useColor = term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""

func col(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isPhoneLike(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(unicode.IsDigit(r) || strings.ContainsRune("+-() ", r)) {
			return false
		}
	}
	return len(digitsOnly(s)) >= 7
}

func userPart(jid string) string {
	if i := strings.Index(jid, "@"); i >= 0 {
		return jid[:i]
	}
	return jid
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func expandHome(p string) string {
	if p == "~" {
		return homeDir()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}

func fmtTime(ts int64) string {
	t := time.Unix(ts, 0)
	n := time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := n.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return t.Format("15:04")
	}
	return t.Format("Jan 02 15:04")
}

func isSubseq(q, s string) bool {
	qr := []rune(q)
	i := 0
	for _, r := range s {
		if i < len(qr) && r == qr[i] {
			i++
		}
	}
	return i == len(qr)
}

// fuzzyScore: 100 exact, 80 prefix, 70 word-prefix, 60 substring, 30 subsequence.
func fuzzyScore(q, name string) int {
	q = strings.ToLower(strings.TrimSpace(q))
	n := strings.ToLower(name)
	if q == "" || n == "" {
		return 0
	}
	if n == q {
		return 100
	}
	if strings.HasPrefix(n, q) {
		return 80
	}
	for _, w := range strings.Fields(n) {
		if strings.HasPrefix(w, q) {
			return 70
		}
	}
	if strings.Contains(n, q) {
		return 60
	}
	if len([]rune(q)) >= 3 && isSubseq(q, n) {
		return 30
	}
	return 0
}

// notify shows an Android notification through Termux:API (if installed).
func notify(chat, title, body string) {
	bin, err := exec.LookPath("termux-notification")
	if err != nil {
		return
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(chat))
	cmd := exec.Command(bin,
		"--id", fmt.Sprint(h.Sum32()%100000),
		"--group", "wacli",
		"--title", title,
		"--content", body)
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// askPass asks for the vault passphrase (and confirmation on first run).
func askPass() (string, error) {
	if !vaultExists() {
		fmt.Fprintln(os.Stderr, "First run: choose a passphrase to encrypt your WhatsApp data.")
		fmt.Fprintln(os.Stderr, "It cannot be recovered if you forget it.")
		p1, err := readSecret("New passphrase: ")
		if err != nil {
			return "", err
		}
		if len(p1) < 6 {
			return "", errors.New("passphrase must be at least 6 characters")
		}
		p2, err := readSecret("Repeat passphrase: ")
		if err != nil {
			return "", err
		}
		if p1 != p2 {
			return "", errors.New("passphrases do not match")
		}
		return p1, nil
	}
	return readSecret("Passphrase: ")
}
