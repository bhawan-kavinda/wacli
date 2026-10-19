package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/crypto/argon2"
)

// The whole SQLite database (WhatsApp keys + messages) is kept encrypted at
// rest as ~/.wacli/wacli.db.enc (AES-256-GCM, key = argon2id(passphrase)).
// While wacli runs, a decrypted working copy lives in the app-private temp dir
// and is re-encrypted every 2 minutes and on exit.

var (
	vaultMagic  = []byte("WACLIv1\n")
	ErrWrongPass = errors.New("wrong passphrase (or corrupted vault)")
	ErrLocked    = errors.New("another wacli process is using the database (is the daemon running? try: wacli daemon stop)")
)

type Vault struct {
	runPath string
	lockF   *os.File
	key     []byte
	salt    []byte
}

func vaultExists() bool {
	_, err := os.Stat(encPath())
	return err == nil
}

func deriveKey(pass string, salt []byte) []byte {
	return argon2.IDKey([]byte(pass), salt, 3, 32*1024, 2, 32)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

func encryptBlob(key, salt, plain []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(vaultMagic)+len(salt)+len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, vaultMagic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plain, vaultMagic)
	return out, nil
}

func decryptBlob(pass string, data []byte) (plain, key, salt []byte, err error) {
	ml := len(vaultMagic)
	if len(data) < ml+16+12 || !bytes.HasPrefix(data, vaultMagic) {
		return nil, nil, nil, errors.New("not a wacli vault file")
	}
	salt = data[ml : ml+16]
	nonce := data[ml+16 : ml+28]
	ct := data[ml+28:]
	key = deriveKey(pass, salt)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, nil, err
	}
	plain, err = gcm.Open(nil, nonce, ct, vaultMagic)
	if err != nil {
		return nil, nil, nil, ErrWrongPass
	}
	return plain, key, salt, nil
}

// CheckPass verifies a passphrase without opening the vault.
func CheckPass(pass string) error {
	data, err := os.ReadFile(encPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, _, _, err = decryptBlob(pass, data)
	return err
}

func OpenVault(pass string) (*Vault, error) {
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(filepath.Join(dataDir(), "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lf.Close()
		return nil, ErrLocked
	}
	v := &Vault{lockF: lf}
	runDir := filepath.Join(os.TempDir(), fmt.Sprintf("wacli-%d", os.Getuid()))
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		v.release()
		return nil, err
	}
	v.runPath = filepath.Join(runDir, "wacli.db")

	data, err := os.ReadFile(encPath())
	switch {
	case err == nil:
		plain, key, salt, derr := decryptBlob(pass, data)
		if derr != nil {
			v.release()
			return nil, derr
		}
		v.key, v.salt = key, salt
		// If a working copy is left over from a crash it is newer: keep it.
		if _, serr := os.Stat(v.runPath); serr != nil {
			if werr := os.WriteFile(v.runPath, plain, 0o600); werr != nil {
				v.release()
				return nil, werr
			}
		}
	case os.IsNotExist(err):
		v.salt = make([]byte, 16)
		if _, rerr := rand.Read(v.salt); rerr != nil {
			v.release()
			return nil, rerr
		}
		v.key = deriveKey(pass, v.salt)
	default:
		v.release()
		return nil, err
	}
	return v, nil
}

// Seal writes a consistent encrypted snapshot of the database to disk.
func (v *Vault) Seal(db *sql.DB) error {
	snap := v.runPath + ".snap"
	_ = os.Remove(snap)
	q := fmt.Sprintf("VACUUM INTO '%s'", strings.ReplaceAll(snap, "'", "''"))
	if _, err := db.Exec(q); err != nil {
		return err
	}
	defer os.Remove(snap)
	plain, err := os.ReadFile(snap)
	if err != nil {
		return err
	}
	blob, err := encryptBlob(v.key, v.salt, plain)
	if err != nil {
		return err
	}
	tmp := encPath() + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, encPath())
}

func (v *Vault) release() {
	if v.lockF != nil {
		_ = syscall.Flock(int(v.lockF.Fd()), syscall.LOCK_UN)
		v.lockF.Close()
		v.lockF = nil
	}
}

// Close seals, closes the db and removes the plaintext working copy.
func (v *Vault) Close(db *sql.DB) {
	sealed := true
	if db != nil {
		if err := v.Seal(db); err != nil {
			sealed = false
			fmt.Fprintln(os.Stderr, "warning: could not encrypt database:", err)
			fmt.Fprintln(os.Stderr, "working copy kept at", v.runPath)
		}
		db.Close()
	}
	if sealed {
		_ = os.Remove(v.runPath)
	}
	v.release()
}
