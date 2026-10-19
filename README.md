# wacli — WhatsApp CLI + TUI for Termux (Go, whatsmeow)

Unofficial: uses the WhatsApp Web multi-device protocol via
[whatsmeow](https://github.com/tulir/whatsmeow). Automated clients can in
theory get an account restricted — use it for your own personal chats.

## Install (Termux)

    pkg update && pkg install golang clang git termux-api unzip
    termux-setup-storage            # gives ~/storage/downloads (media goes to .../wacli)
    # install the "Termux:API" app too (F-Droid) for notifications

    unzip ~/storage/downloads/wacli.zip -d ~ && cd ~/wacli
    go mod tidy                     # downloads dependencies (needs internet, first time is slow)
    go build -o $PREFIX/bin/wacli . # cgo sqlite compiles for a few minutes the first time

## First run

    wacli login 94771234567         # your number with country code
    # -> choose a passphrase (encrypts everything), get an 8-char pairing code
    # -> WhatsApp > Settings > Linked devices > Link a device >
    #    "Link with phone number instead" > type the code

    wacli daemon start              # asks passphrase once, stays connected, notifications on
    wacli                           # TUI (talks to the daemon, no passphrase prompt)

Without the daemon every command asks the passphrase, connects, does its job,
disconnects. With it, commands are instant.

## Commands

    wacli chats | contacts [q] | groups [name] | msgs <chat> -n 30
    wacli send mom "hello"          wacli send mom - < note.txt
    wacli send mom "yes" --reply 3EB0A1B2
    wacli sendfile mom ~/pic.jpg "caption"
    wacli get mom 3EB0A1B2          # saves to ~/storage/downloads/wacli
    wacli react mom 3EB0A1B2 👍     wacli edit mom 3EB0A1B2 "fixed"     wacli rm mom 3EB0A1B2
    wacli read mom                  # blue ticks
    wacli presence mom              wacli watch [chat]
    wacli alias set mom 94771234567
    wacli config set notifications off     (also: send_read_receipts, send_typing, download_dir)

Add `--json` to list/show commands for scripting. `<chat>` can be an alias,
a (fuzzy) contact/group name, a phone number or a JID.

## TUI keys

List: j/k or ↑/↓ move · Enter open · q quit.
Chat: type + Enter send · ↑/↓ PgUp/PgDn scroll · Esc/Tab back to list.
Slash commands (N = the message number shown at the start of each line):
`/r N text` reply · `/react N 👍` · `/edit N text` · `/del N` · `/get N` ·
`/file path [caption]` · `/help`.
Ticks: ✓ sent · ✓✓ delivered · ✔✔ read.
Screens narrower than 70 columns show one pane at a time.

## Security model

Everything (WhatsApp keys + all messages) is one SQLite DB, stored on disk only as
`~/.wacli/wacli.db.enc` (AES-256-GCM, key = argon2id(passphrase)). While wacli
runs, a decrypted working copy lives in Termux's private temp dir and is
re-encrypted every 2 minutes and on exit. After a hard kill a plaintext copy can
remain in `$TMPDIR/wacli-<uid>/` until the next start — then it is picked up
and re-encrypted. Forgetting the passphrase = data lost (just `rm -r ~/.wacli`
and log in again).

## Known limits

* Chat list is built from messages that arrive/are sent while wacli runs;
  WhatsApp's old history is not imported. Contacts and groups come from WhatsApp.
* Media can only be downloaded for messages received while wacli was running.
* Presence (online/last seen) is only visible for contacts that allow it.
* Debug: `WACLI_DEBUG=1 wacli ...`; daemon log: `~/.wacli/daemon.log`.
