package main

import "strings"

// Empty is used for RPC calls with no arguments / no result.
type Empty struct{}

type Status struct {
	LoggedIn  bool   `json:"logged_in"`
	Connected bool   `json:"connected"`
	Phone     string `json:"phone"`
	PushName  string `json:"push_name"`
	Mode      string `json:"mode"`
}

type Chat struct {
	JID      string `json:"jid"`
	Name     string `json:"name"`
	IsGroup  bool   `json:"is_group"`
	LastTS   int64  `json:"last_ts"`
	LastText string `json:"last_text"`
	Unread   int    `json:"unread"`
}

func (c Chat) Title() string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	return userPart(c.JID)
}

type Message struct {
	Chat       string `json:"chat"`
	ID         string `json:"id"`
	Sender     string `json:"sender"`
	SenderName string `json:"sender_name"`
	FromMe     bool   `json:"from_me"`
	TS         int64  `json:"ts"`
	Text       string `json:"text"`
	Kind       string `json:"kind"` // text,image,video,audio,document,sticker,location
	Filename   string `json:"filename,omitempty"`
	Status     int    `json:"status"` // 0 sent, 1 delivered, 2 read
	Deleted    bool   `json:"deleted,omitempty"`
	Edited     bool   `json:"edited,omitempty"`
	ReplyTo    string `json:"reply_to,omitempty"`
	Reactions  string `json:"reactions,omitempty"`
	HasMedia   bool   `json:"has_media,omitempty"`
}

type Contact struct {
	JID   string `json:"jid"`
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
}

type Member struct {
	JID   string `json:"jid"`
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
}

type Group struct {
	JID     string   `json:"jid"`
	Name    string   `json:"name"`
	Topic   string   `json:"topic,omitempty"`
	Members []Member `json:"members"`
}

type PresenceInfo struct {
	Known    bool  `json:"known"`
	Online   bool  `json:"online"`
	LastSeen int64 `json:"last_seen,omitempty"`
}

type MsgQuery struct {
	Chat  string `json:"chat"`
	Limit int    `json:"limit"`
}

type SendArgs struct {
	Chat    string `json:"chat"`
	Text    string `json:"text"`
	ReplyTo string `json:"reply_to,omitempty"`
}

type FileArgs struct {
	Chat    string `json:"chat"`
	Path    string `json:"path"`
	Caption string `json:"caption,omitempty"`
	ReplyTo string `json:"reply_to,omitempty"`
}

type SendResult struct {
	ID   string `json:"id"`
	Chat string `json:"chat"`
}

type DlArgs struct {
	Chat string `json:"chat"`
	ID   string `json:"id"`
	Dir  string `json:"dir,omitempty"`
}

type ReactArgs struct {
	Chat  string `json:"chat"`
	ID    string `json:"id"`
	Emoji string `json:"emoji"`
}

type EditArgs struct {
	Chat string `json:"chat"`
	ID   string `json:"id"`
	Text string `json:"text"`
}

// SearchArgs / SearchHit: full-text search over stored messages.
type SearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type SearchHit struct {
	Chat   string `json:"chat"`
	ID     string `json:"id"`
	Sender string `json:"sender"` // display name, or the sender's JID if no name is known
	FromMe bool   `json:"from_me"`
	TS     int64  `json:"ts"`
	Text   string `json:"text"`
}

type MsgRef struct {
	Chat string `json:"chat"`
	ID   string `json:"id"`
}

type TypingArgs struct {
	Chat string `json:"chat"`
	On   bool   `json:"on"`
}

// Event is pushed to subscribers (TUI, `wacli watch`).
// Types: connected, disconnected, message, update, presence, typing.
type Event struct {
	Type string   `json:"type"`
	Chat string   `json:"chat,omitempty"`
	From string   `json:"from,omitempty"`
	On   bool     `json:"on,omitempty"`
	Msg  *Message `json:"msg,omitempty"`
}

// Backend is what the CLI commands and the TUI talk to. It is implemented by
// Engine (in-process) and Remote (talks to the background daemon over a unix socket).
type Backend interface {
	Status() (Status, error)
	Chats() ([]Chat, error)
	Messages(MsgQuery) ([]Message, error)
	Search(SearchArgs) ([]SearchHit, error)
	Contacts() ([]Contact, error)
	Groups() ([]Group, error)
	Resolve(string) (string, error)
	Send(SendArgs) (SendResult, error)
	SendFile(FileArgs) (SendResult, error)
	Download(DlArgs) (string, error)
	React(ReactArgs) error
	Edit(EditArgs) error
	Delete(MsgRef) error
	MarkRead(string) error
	Typing(TypingArgs) error
	Presence(string) (PresenceInfo, error)
	SetOnline(bool) error
	SetActive(string) error
	Subscribe() (<-chan Event, func(), error)
	Close() error
}

var (
	_ Backend = (*Engine)(nil)
	_ Backend = (*Remote)(nil)
)
