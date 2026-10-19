<p align="center">
  <img src="assets/banner.svg" alt="wacli: WhatsApp from your terminal" width="100%">
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: MPL-2.0" src="https://img.shields.io/badge/license-MPL--2.0-00A884?style=flat-square"></a>
  <img alt="Written in Go" src="https://img.shields.io/badge/Go-00ADD8?style=flat-square&logo=go&logoColor=white">
  <img alt="Made for Termux" src="https://img.shields.io/badge/made%20for-Termux-1E1E1E?style=flat-square">
  <img alt="Encrypted at rest" src="https://img.shields.io/badge/encrypted-AES--256--GCM-005C4B?style=flat-square">
</p>

<p align="center">
  Chat, send files and get notified, all from a terminal on your phone.<br>
  Built on <a href="https://github.com/tulir/whatsmeow">whatsmeow</a> and the WhatsApp Web multi-device protocol.
</p>

> [!WARNING]
> wacli is an **unofficial** project and is not affiliated with or endorsed by WhatsApp or Meta.
> Automated clients can get an account restricted, so use it for your own personal chats.

## Features

- **Full-screen TUI** with chats, groups, channels, pins, drafts, global search and five themes
- **Touch friendly** and **smooth looking**, with optional rounded pills
- **Scriptable CLI** with `--json` output
- **Pairing code login**, no QR scan needed
- **Background daemon** with Termux notifications
- **Encrypted storage** (AES-256-GCM, passphrase protected)

## Install

Needs Termux (F-Droid), Go 1.26+ and a C compiler.

```bash
pkg update && pkg install golang clang git termux-api
termux-setup-storage

git clone https://github.com/bhawan-kavinda/wacli.git && cd wacli
go mod tidy
go build -o $PREFIX/bin/wacli .
```

The first build compiles SQLite and can take a few minutes. For notifications, also install the **Termux:API** app.

## Quick start

```bash
wacli login 94771234567     # your number with country code
```

Enter the 8-character code in WhatsApp under **Settings → Linked devices → Link a device → Link with phone number instead**.

```bash
wacli daemon start          # stay connected
wacli                       # open the TUI
```

## Commands

```bash
wacli chats                            # list chats
wacli msgs mom -n 30                   # read messages
wacli send mom "hello"                 # send text
wacli sendfile mom ~/pic.jpg "caption"
wacli get mom 3EB0A1B2                 # download media
wacli react mom 3EB0A1B2 👍
wacli watch                            # live stream
```

Also: `contacts`, `groups`, `edit`, `rm`, `read`, `presence`, `alias`, `config`. Run `wacli help` for everything. `<chat>` can be an alias, a name, a phone number or a JID.

## TUI keys

| Where | Keys |
| --- | --- |
| List | `j` `k` move · `Enter` open · `1`-`4` tabs · `c` contacts · `p` pin · `/` search · `t` theme · `q` quit |
| Chat | type + `Enter` send · `PgUp` `PgDn` scroll · `Esc` back |
| Slash | `/r N text` · `/react N 👍` · `/edit N text` · `/del N` · `/get N` · `/file path` · `/theme` · `/help` |
| Touch | tap chats, tabs and buttons · swipe to scroll |

## Settings

`wacli config set <key> <value>`, stored in `~/.wacli/config.json`.

| Key | Default | Meaning |
| --- | --- | --- |
| `notifications` | `true` | Termux notifications |
| `send_read_receipts` | `true` | Send blue ticks |
| `send_typing` | `true` | Show typing to others |
| `rounded_caps` | `false` | Rounded pills, needs a Nerd Font |
| `download_dir` | `~/storage/downloads/wacli` | Media folder |

**Rounded pills:** install a Nerd Font (`cp YourNerdFont.ttf ~/.termux/font.ttf && termux-reload-settings`), then `wacli config set rounded_caps true`. Without that font they show as empty boxes.
Other options: `WACLI_ROUNDED=1` (one run), `WACLI_256=1` (own colour profile), `WACLI_DEBUG=1`, `WACLI_HOME=<dir>`.

## Security

Data is kept only as `~/.wacli/wacli.db.enc` (AES-256-GCM, key from your passphrase via argon2id). A decrypted working copy lives in Termux's private temp folder while wacli runs and is re-encrypted every 2 minutes and on exit.
**Forget the passphrase and the data is gone.**

## Good to know

- Only messages sent or received while wacli runs are stored. Old history is not imported.
- Debug with `WACLI_DEBUG=1`, log at `~/.wacli/daemon.log`.

## Contribute

wacli is built by people who like living in the terminal, and **you are welcome to build it with us**. Whether you fix a typo, report a bug or add a whole feature, it all helps.

- 🐛 **[Issues](https://github.com/bhawan-kavinda/wacli/issues)** are open. Report bugs, ask for features, or pick one and help fix it.
- 🔀 **[Pull requests](https://github.com/bhawan-kavinda/wacli/pulls)** are open. Small or big, we will review them.
- 💬 **[Discussions](https://github.com/bhawan-kavinda/wacli/discussions)** are open. Share ideas, ask questions and show how you use wacli.

Please help us make wacli better together. Run `gofmt -w .` and `go vet ./...` before opening a PR, and keep this README in sync when you add a command or setting.

## License

[Mozilla Public License 2.0](LICENSE). Built on [whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0) and [Charm](https://charm.sh). Unofficial project, not affiliated with WhatsApp or Meta. WhatsApp is a trademark of Meta Platforms, Inc.
