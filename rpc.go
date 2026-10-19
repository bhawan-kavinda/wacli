package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"
)

// Newline-delimited JSON over a unix socket (~/.wacli/wacli.sock, mode 0600).

type Req struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Resp struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Event  *Event          `json:"event,omitempty"`
}

func daemonAlive() bool {
	c, err := net.DialTimeout("unix", sockPath(), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// ---------------------------------------------------------------- server

func (e *Engine) Serve(shutdown chan<- struct{}) (net.Listener, error) {
	_ = os.Remove(sockPath())
	ln, err := net.Listen("unix", sockPath())
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(sockPath(), 0o600)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go e.serveConn(c, shutdown)
		}
	}()
	return ln, nil
}

func (e *Engine) serveConn(c net.Conn, shutdown chan<- struct{}) {
	defer c.Close()
	r := bufio.NewReaderSize(c, 64*1024)
	enc := json.NewEncoder(c)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req Req
		if json.Unmarshal(line, &req) != nil {
			return
		}
		if req.Method == "subscribe" {
			ch, cancel, _ := e.Subscribe()
			defer cancel()
			if enc.Encode(Resp{ID: req.ID}) != nil {
				return
			}
			for ev := range ch {
				ev := ev
				if enc.Encode(Resp{Event: &ev}) != nil {
					return
				}
			}
			return
		}
		res, err := e.dispatch(req, shutdown)
		resp := Resp{ID: req.ID}
		if err != nil {
			resp.Error = err.Error()
		} else if b, merr := json.Marshal(res); merr == nil {
			resp.Result = b
		}
		if enc.Encode(resp) != nil {
			return
		}
	}
}

func call1[P, R any](raw json.RawMessage, f func(P) (R, error)) (any, error) {
	var p P
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
	}
	return f(p)
}

func (e *Engine) dispatch(req Req, shutdown chan<- struct{}) (any, error) {
	p := req.Params
	switch req.Method {
	case "status":
		return call1(p, func(Empty) (Status, error) { return e.Status() })
	case "chats":
		return call1(p, func(Empty) ([]Chat, error) { return e.Chats() })
	case "contacts":
		return call1(p, func(Empty) ([]Contact, error) { return e.Contacts() })
	case "groups":
		return call1(p, func(Empty) ([]Group, error) { return e.Groups() })
	case "messages":
		return call1(p, e.Messages)
	case "resolve":
		return call1(p, e.Resolve)
	case "send":
		return call1(p, e.Send)
	case "sendfile":
		return call1(p, e.SendFile)
	case "download":
		return call1(p, e.Download)
	case "presence":
		return call1(p, e.Presence)
	case "react":
		return call1(p, func(a ReactArgs) (Empty, error) { return Empty{}, e.React(a) })
	case "edit":
		return call1(p, func(a EditArgs) (Empty, error) { return Empty{}, e.Edit(a) })
	case "delete":
		return call1(p, func(a MsgRef) (Empty, error) { return Empty{}, e.Delete(a) })
	case "markread":
		return call1(p, func(s string) (Empty, error) { return Empty{}, e.MarkRead(s) })
	case "typing":
		return call1(p, func(a TypingArgs) (Empty, error) { return Empty{}, e.Typing(a) })
	case "online":
		return call1(p, func(b bool) (Empty, error) { return Empty{}, e.SetOnline(b) })
	case "active":
		return call1(p, func(s string) (Empty, error) { return Empty{}, e.SetActive(s) })
	case "shutdown":
		go func() {
			time.Sleep(300 * time.Millisecond)
			select {
			case shutdown <- struct{}{}:
			default:
			}
		}()
		return Empty{}, nil
	}
	return nil, errors.New("unknown method: " + req.Method)
}

// ---------------------------------------------------------------- client

type Remote struct{ sock string }

func newRemote() *Remote { return &Remote{sock: sockPath()} }

func (r *Remote) do(method string, params any, out any) error {
	c, err := net.DialTimeout("unix", r.sock, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(c).Encode(Req{Method: method, Params: b}); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp Resp
	if err := json.Unmarshal(line, &resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if out != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

func rcall[R any](r *Remote, method string, params any) (R, error) {
	var out R
	err := r.do(method, params, &out)
	return out, err
}

func (r *Remote) Status() (Status, error) {
	s, err := rcall[Status](r, "status", nil)
	s.Mode = "daemon"
	return s, err
}
func (r *Remote) Chats() ([]Chat, error)       { return rcall[[]Chat](r, "chats", nil) }
func (r *Remote) Contacts() ([]Contact, error) { return rcall[[]Contact](r, "contacts", nil) }
func (r *Remote) Groups() ([]Group, error)     { return rcall[[]Group](r, "groups", nil) }
func (r *Remote) Messages(q MsgQuery) ([]Message, error) {
	return rcall[[]Message](r, "messages", q)
}
func (r *Remote) Resolve(s string) (string, error)     { return rcall[string](r, "resolve", s) }
func (r *Remote) Send(a SendArgs) (SendResult, error)  { return rcall[SendResult](r, "send", a) }
func (r *Remote) SendFile(a FileArgs) (SendResult, error) {
	return rcall[SendResult](r, "sendfile", a)
}
func (r *Remote) Download(a DlArgs) (string, error)         { return rcall[string](r, "download", a) }
func (r *Remote) React(a ReactArgs) error                   { return r.do("react", a, nil) }
func (r *Remote) Edit(a EditArgs) error                     { return r.do("edit", a, nil) }
func (r *Remote) Delete(a MsgRef) error                     { return r.do("delete", a, nil) }
func (r *Remote) MarkRead(chat string) error                { return r.do("markread", chat, nil) }
func (r *Remote) Typing(a TypingArgs) error                 { return r.do("typing", a, nil) }
func (r *Remote) SetOnline(on bool) error                   { return r.do("online", on, nil) }
func (r *Remote) SetActive(chat string) error               { return r.do("active", chat, nil) }
func (r *Remote) Presence(chat string) (PresenceInfo, error) { return rcall[PresenceInfo](r, "presence", chat) }
func (r *Remote) Close() error                              { return nil }

func (r *Remote) Subscribe() (<-chan Event, func(), error) {
	c, err := net.DialTimeout("unix", r.sock, 3*time.Second)
	if err != nil {
		return nil, nil, err
	}
	if err := json.NewEncoder(c).Encode(Req{Method: "subscribe"}); err != nil {
		c.Close()
		return nil, nil, err
	}
	rd := bufio.NewReaderSize(c, 64*1024)
	if _, err := rd.ReadBytes('\n'); err != nil { // ack
		c.Close()
		return nil, nil, err
	}
	ch := make(chan Event, 128)
	go func() {
		defer close(ch)
		for {
			line, err := rd.ReadBytes('\n')
			if err != nil {
				return
			}
			var resp Resp
			if json.Unmarshal(line, &resp) != nil || resp.Event == nil {
				continue
			}
			select {
			case ch <- *resp.Event:
			default:
			}
		}
	}()
	return ch, func() { c.Close() }, nil
}
