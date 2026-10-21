package devsync

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// KDFIterations is the PBKDF2-HMAC-SHA256 work factor for the passphrase
// (tests lower it). Changing it changes every derived key.
var KDFIterations = 600_000

// Keys are derived from the username and sync passphrase on the device.
// Auth is sent to the server (which stores a salted hash of it); Enc never
// leaves the device. Neither reveals the other or the passphrase.
type Keys struct {
	Auth []byte `json:"auth"`
	Enc  []byte `json:"enc"`
}

// DeriveKeys stretches the passphrase (salted with the username) and splits
// the result into the auth and encryption keys with HKDF.
func DeriveKeys(username, passphrase string) (Keys, error) {
	master, err := pbkdf2.Key(sha256.New, passphrase, []byte("gomail-sync/v1\x00"+username), KDFIterations, 32)
	if err != nil {
		return Keys{}, err
	}
	auth, err := hkdf.Key(sha256.New, master, nil, "gomail-sync/v1 auth", 32)
	if err != nil {
		return Keys{}, err
	}
	enc, err := hkdf.Key(sha256.New, master, nil, "gomail-sync/v1 encryption", 32)
	if err != nil {
		return Keys{}, err
	}
	return Keys{Auth: auth, Enc: enc}, nil
}

// Sealed document layout: version byte, 12-byte nonce, AES-256-GCM
// ciphertext of the gzip-compressed document. The document name is
// authenticated too, so the server can't swap one document for another.
const sealVersion = 1

// ErrDecrypt means a document couldn't be decrypted: another passphrase
// encrypted it, or it was damaged or tampered with.
var ErrDecrypt = errors.New("a synced document couldn't be decrypted (was it encrypted with another passphrase?)")

func aad(name string) []byte { return []byte("gomail-sync/v1 doc:" + name) }

func aead(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal compresses and encrypts a document's plaintext.
func Seal(key []byte, name string, plaintext []byte) ([]byte, error) {
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	if _, err := zw.Write(plaintext); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	g, err := aead(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+g.NonceSize(), 1+g.NonceSize()+zb.Len()+g.Overhead())
	out[0] = sealVersion
	if _, err := rand.Read(out[1:]); err != nil {
		return nil, err
	}
	return g.Seal(out, out[1:], zb.Bytes(), aad(name)), nil
}

// maxPlaintext bounds a decompressed document.
const maxPlaintext = 256 << 20

// Open decrypts and decompresses a sealed document.
func Open(key []byte, name string, sealed []byte) ([]byte, error) {
	g, err := aead(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < 1+g.NonceSize()+g.Overhead() || sealed[0] != sealVersion {
		return nil, ErrDecrypt
	}
	zipped, err := g.Open(nil, sealed[1:1+g.NonceSize()], sealed[1+g.NonceSize():], aad(name))
	if err != nil {
		return nil, ErrDecrypt
	}
	zr, err := gzip.NewReader(bytes.NewReader(zipped))
	if err != nil {
		return nil, ErrDecrypt
	}
	data, err := io.ReadAll(io.LimitReader(zr, maxPlaintext+1))
	if err != nil || len(data) > maxPlaintext {
		return nil, ErrDecrypt
	}
	return data, nil
}
