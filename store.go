package main

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func openDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_foreign_keys=on&_busy_timeout=10000&_journal_mode=DELETE"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS wacli_chats (
  jid TEXT PRIMARY KEY,
  is_group INTEGER NOT NULL DEFAULT 0,
  last_ts INTEGER NOT NULL DEFAULT 0,
  last_text TEXT NOT NULL DEFAULT '',
  unread INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS wacli_names (
  jid TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  prio INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS wacli_messages (
  chat TEXT NOT NULL,
  id TEXT NOT NULL,
  sender TEXT NOT NULL DEFAULT '',
  sender_name TEXT NOT NULL DEFAULT '',
  from_me INTEGER NOT NULL DEFAULT 0,
  ts INTEGER NOT NULL DEFAULT 0,
  text TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT 'text',
  filename TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL DEFAULT 0,
  deleted INTEGER NOT NULL DEFAULT 0,
  edited INTEGER NOT NULL DEFAULT 0,
  reply_to TEXT NOT NULL DEFAULT '',
  read_sent INTEGER NOT NULL DEFAULT 0,
  raw BLOB,
  PRIMARY KEY (chat, id)
);
CREATE INDEX IF NOT EXISTS wacli_msg_ts ON wacli_messages(chat, ts);
CREATE TABLE IF NOT EXISTS wacli_reactions (
  chat TEXT NOT NULL,
  msg_id TEXT NOT NULL,
  sender TEXT NOT NULL,
  emoji TEXT NOT NULL,
  PRIMARY KEY (chat, msg_id, sender)
);
`

type Store struct{ db *sql.DB }

func (s *Store) init() error {
	_, err := s.db.Exec(schema)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) insertMessage(m *Message, raw []byte) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO wacli_messages
		(chat,id,sender,sender_name,from_me,ts,text,kind,filename,status,reply_to,raw)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Chat, m.ID, m.Sender, m.SenderName, b2i(m.FromMe), m.TS, m.Text, m.Kind, m.Filename, m.Status, m.ReplyTo, raw)
	return err
}

func (s *Store) touchChat(jid string, isGroup bool, ts int64, preview string, unreadInc int) error {
	_, err := s.db.Exec(`INSERT INTO wacli_chats(jid,is_group,last_ts,last_text,unread) VALUES (?,?,?,?,?)
		ON CONFLICT(jid) DO UPDATE SET
		  last_text = CASE WHEN excluded.last_ts >= wacli_chats.last_ts THEN excluded.last_text ELSE wacli_chats.last_text END,
		  last_ts = MAX(wacli_chats.last_ts, excluded.last_ts),
		  unread = wacli_chats.unread + excluded.unread`,
		jid, b2i(isGroup), ts, preview, unreadInc)
	return err
}

func (s *Store) setName(jid, name string, prio int) {
	if jid == "" || strings.TrimSpace(name) == "" {
		return
	}
	_, _ = s.db.Exec(`INSERT INTO wacli_names(jid,name,prio) VALUES (?,?,?)
		ON CONFLICT(jid) DO UPDATE SET name=excluded.name, prio=excluded.prio
		WHERE excluded.prio >= wacli_names.prio`, jid, name, prio)
}

func (s *Store) name(jid string) string {
	var n string
	if err := s.db.QueryRow(`SELECT name FROM wacli_names WHERE jid=?`, jid).Scan(&n); err != nil {
		return ""
	}
	return n
}

func (s *Store) allNames() map[string]string {
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT jid, name FROM wacli_names WHERE name != ''`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var j, n string
		if rows.Scan(&j, &n) == nil {
			out[j] = n
		}
	}
	return out
}

func (s *Store) hasChat(jid string) bool {
	var x int
	return s.db.QueryRow(`SELECT 1 FROM wacli_chats WHERE jid=?`, jid).Scan(&x) == nil
}

func (s *Store) chats() ([]Chat, error) {
	rows, err := s.db.Query(`SELECT c.jid, COALESCE(n.name,''), c.is_group, c.last_ts, c.last_text, c.unread
		FROM wacli_chats c LEFT JOIN wacli_names n ON n.jid = c.jid
		ORDER BY c.last_ts DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		var c Chat
		var g int
		if err := rows.Scan(&c.JID, &c.Name, &g, &c.LastTS, &c.LastText, &c.Unread); err != nil {
			return nil, err
		}
		c.IsGroup = g == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) messages(chat string, limit int) ([]Message, error) {
	rows, err := s.db.Query(`SELECT m.chat, m.id, m.sender, COALESCE(NULLIF(n.name,''), m.sender_name),
		m.from_me, m.ts, m.text, m.kind, m.filename, m.status, m.deleted, m.edited, m.reply_to,
		COALESCE((SELECT GROUP_CONCAT(emoji,'') FROM wacli_reactions r WHERE r.chat=m.chat AND r.msg_id=m.id),''),
		(m.raw IS NOT NULL)
		FROM wacli_messages m LEFT JOIN wacli_names n ON n.jid = m.sender
		WHERE m.chat = ? ORDER BY m.ts DESC, m.rowid DESC LIMIT ?`, chat, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var fm, del, ed, hm int
		if err := rows.Scan(&m.Chat, &m.ID, &m.Sender, &m.SenderName, &fm, &m.TS, &m.Text, &m.Kind,
			&m.Filename, &m.Status, &del, &ed, &m.ReplyTo, &m.Reactions, &hm); err != nil {
			return nil, err
		}
		m.FromMe, m.Deleted, m.Edited, m.HasMedia = fm == 1, del == 1, ed == 1, hm == 1
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if out == nil {
		out = []Message{}
	}
	return out, nil
}

// search finds messages whose text contains q (case-insensitive for ASCII), newest first.
func (s *Store) search(q string, limit int) ([]SearchHit, error) {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	rows, err := s.db.Query(`SELECT m.chat, m.id, m.ts, m.from_me,
		COALESCE(NULLIF(n.name,''), NULLIF(m.sender_name,''), m.sender), m.text
		FROM wacli_messages m LEFT JOIN wacli_names n ON n.jid = m.sender
		WHERE m.deleted = 0 AND m.text != '' AND m.text LIKE ? ESCAPE '\'
		ORDER BY m.ts DESC LIMIT ?`, "%"+esc+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		var fm int
		if err := rows.Scan(&h.Chat, &h.ID, &h.TS, &fm, &h.Sender, &h.Text); err != nil {
			return nil, err
		}
		h.FromMe = fm == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// getMessage finds a message by full id or id prefix.
func (s *Store) getMessage(chat, id string) (*Message, []byte, error) {
	var m Message
	var raw []byte
	var fm, del, ed int
	err := s.db.QueryRow(`SELECT chat,id,sender,sender_name,from_me,ts,text,kind,filename,status,deleted,edited,reply_to,raw
		FROM wacli_messages WHERE chat=? AND id LIKE ? ORDER BY (id=?) DESC, ts DESC LIMIT 1`,
		chat, id+"%", id).Scan(&m.Chat, &m.ID, &m.Sender, &m.SenderName, &fm, &m.TS, &m.Text, &m.Kind,
		&m.Filename, &m.Status, &del, &ed, &m.ReplyTo, &raw)
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("message %q not found in this chat", id)
	}
	if err != nil {
		return nil, nil, err
	}
	m.FromMe, m.Deleted, m.Edited = fm == 1, del == 1, ed == 1
	return &m, raw, nil
}

func (s *Store) markStatus(ids []string, status int) {
	if len(ids) == 0 {
		return
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := []any{status}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, status)
	_, _ = s.db.Exec(`UPDATE wacli_messages SET status=? WHERE from_me=1 AND id IN (`+ph+`) AND status<?`, args...)
}

func (s *Store) markDeleted(chat, id string) {
	_, _ = s.db.Exec(`UPDATE wacli_messages SET deleted=1, text='' WHERE chat=? AND id=?`, chat, id)
}

func (s *Store) markEdited(chat, id, text string) {
	_, _ = s.db.Exec(`UPDATE wacli_messages SET edited=1, text=? WHERE chat=? AND id=?`, text, chat, id)
}

func (s *Store) setReaction(chat, msgID, sender, emoji string) {
	if emoji == "" {
		_, _ = s.db.Exec(`DELETE FROM wacli_reactions WHERE chat=? AND msg_id=? AND sender=?`, chat, msgID, sender)
		return
	}
	_, _ = s.db.Exec(`INSERT OR REPLACE INTO wacli_reactions(chat,msg_id,sender,emoji) VALUES (?,?,?,?)`,
		chat, msgID, sender, emoji)
}

type pendingRead struct{ ID, Sender string }

func (s *Store) unreadIncoming(chat string) ([]pendingRead, error) {
	rows, err := s.db.Query(`SELECT id, sender FROM wacli_messages WHERE chat=? AND from_me=0 AND read_sent=0`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pendingRead
	for rows.Next() {
		var p pendingRead
		if err := rows.Scan(&p.ID, &p.Sender); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) clearUnread(chat string) {
	_, _ = s.db.Exec(`UPDATE wacli_messages SET read_sent=1 WHERE chat=? AND from_me=0`, chat)
	_, _ = s.db.Exec(`UPDATE wacli_chats SET unread=0 WHERE jid=?`, chat)
}
