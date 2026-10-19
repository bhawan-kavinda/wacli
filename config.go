package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Notifications bool              `json:"notifications"`
	SendReceipts  bool              `json:"send_read_receipts"`
	SendTyping    bool              `json:"send_typing"`
	DownloadDir   string            `json:"download_dir"`
	Theme         string            `json:"theme"`
	Aliases       map[string]string `json:"aliases"`
	RoundedCaps   bool              `json:"rounded_caps"` // smooth rounded pills (needs a Nerd Font / Powerline font)
	Pinned        []string          `json:"pinned"` // chat JIDs pinned (★) to the top of the TUI list
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func dataDir() string {
	if d := os.Getenv("WACLI_HOME"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), ".wacli")
}

func configPath() string { return filepath.Join(dataDir(), "config.json") }
func sockPath() string   { return filepath.Join(dataDir(), "wacli.sock") }
func encPath() string    { return filepath.Join(dataDir(), "wacli.db.enc") }

func defaultDownloadDir() string {
	if st, err := os.Stat(filepath.Join(homeDir(), "storage", "downloads")); err == nil && st.IsDir() {
		return filepath.Join(homeDir(), "storage", "downloads", "wacli")
	}
	return filepath.Join(dataDir(), "media")
}

func loadConfig() *Config {
	c := &Config{
		Notifications: true,
		SendReceipts:  true,
		SendTyping:    true,
		DownloadDir:   defaultDownloadDir(),
		Theme:         "whatsapp",
		Aliases:       map[string]string{},
	}
	if b, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(b, c)
	}
	if c.Aliases == nil {
		c.Aliases = map[string]string{}
	}
	if c.DownloadDir == "" {
		c.DownloadDir = defaultDownloadDir()
	}
	return c
}

func (c *Config) Save() error {
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), b, 0o600)
}

// ---------------------------------------------------------------- drafts

// Unsent text per chat, kept in ~/.wacli/drafts.json so it survives closing the TUI.
func draftsPath() string { return filepath.Join(dataDir(), "drafts.json") }

func loadDrafts() map[string]string {
	out := map[string]string{}
	if b, err := os.ReadFile(draftsPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func saveDrafts(d map[string]string) error {
	if len(d) == 0 {
		_ = os.Remove(draftsPath())
		return nil
	}
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return os.WriteFile(draftsPath(), b, 0o600)
}
