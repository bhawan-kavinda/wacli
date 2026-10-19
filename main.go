package main

import (
	"fmt"
	"os"
)

const usage = `wacli — WhatsApp from your terminal (Termux friendly)

Setup
  wacli login <phone>              link with a pairing code (no QR needed)
  wacli logout                     unlink this device
  wacli daemon start|stop|status   background process: stays connected, sends
                                   Termux notifications, unlocks the DB once

Everyday
  wacli                            interactive TUI (same as: wacli tui)
  wacli chats                      list chats           [--json]
  wacli contacts [filter]          address book         [--json]
  wacli groups [name]              groups / members     [--json]
  wacli msgs <chat> [-n 20]        show messages        [--json]
  wacli send <chat> <text...>      send text (- reads stdin) [--reply <id>]
  wacli sendfile <chat> <path> [caption...]
  wacli get <chat> <msg-id> [--dir DIR]   download media
  wacli react <chat> <msg-id> <emoji>
  wacli edit <chat> <msg-id> <text...>
  wacli rm <chat> <msg-id>         delete for everyone (own messages)
  wacli read <chat>                mark as read (blue ticks)
  wacli presence <chat>            online / last seen
  wacli watch [chat]               live stream of messages, typing, presence

Settings
  wacli alias [list|set <name> <target>|rm <name>]
  wacli config [set <key> <value>]

<chat> = alias | contact/group name (fuzzy) | phone number | JID.
Message ids: the short #xxxxxxxx shown by "msgs" works as-is.
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"tui"}
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
	case "login":
		err = cmdLogin(rest)
	case "logout":
		err = cmdLogout()
	case "daemon":
		err = cmdDaemon(rest)
	case "status":
		err = cmdStatus(rest)
	case "tui":
		err = withBackend(true, runTUI)
	case "chats":
		err = cmdChats(rest)
	case "contacts":
		err = cmdContacts(rest)
	case "groups":
		err = cmdGroups(rest)
	case "msgs":
		err = cmdMsgs(rest)
	case "send":
		err = cmdSend(rest)
	case "sendfile":
		err = cmdSendFile(rest)
	case "get":
		err = cmdGet(rest)
	case "react":
		err = cmdReact(rest)
	case "edit":
		err = cmdEdit(rest)
	case "rm":
		err = cmdRm(rest)
	case "read":
		err = cmdRead(rest)
	case "presence":
		err = cmdPresence(rest)
	case "watch":
		err = cmdWatch(rest)
	case "alias":
		err = cmdAlias(rest)
	case "config":
		err = cmdConfig(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
