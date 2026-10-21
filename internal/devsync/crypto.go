package devsync

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// Keys are derived from the sync code on the device. Account names the
// sync account on the server; Auth is sent to the server (which stores a
// salted hash of it); Enc never leaves the device. None of them reveals the
// code or another key. The code has 256 random bits, so HKDF needs no slow
// stretching.
type Keys struct {
	Account string `json:"account"`
	Auth    []byte `json:"auth"`
	Enc     []byte `json:"enc"`
}

// DeriveKeys derives the keys of a sync code's entropy (ParseCode).
func DeriveKeys(entropy []byte) (Keys, error) {
	if len(entropy) != 32 {
		return Keys{}, errors.New("invalid sync code")
	}
	id, err := hkdf.Key(sha256.New, entropy, nil, "gomail-sync/v1 account", 16)
	if err != nil {
		return Keys{}, err
	}
	auth, err := hkdf.Key(sha256.New, entropy, nil, "gomail-sync/v1 auth", 32)
	if err != nil {
		return Keys{}, err
	}
	enc, err := hkdf.Key(sha256.New, entropy, nil, "gomail-sync/v1 encryption", 32)
	if err != nil {
		return Keys{}, err
	}
	return Keys{Account: hex.EncodeToString(id), Auth: auth, Enc: enc}, nil
}

// KeysFromCode parses a code and derives its keys.
func KeysFromCode(code string) (Keys, string, error) {
	entropy, canonical, err := ParseCode(code)
	if err != nil {
		return Keys{}, "", err
	}
	k, err := DeriveKeys(entropy)
	return k, canonical, err
}

// Sealed document layout: version byte, 12-byte nonce, AES-256-GCM
// ciphertext of the gzip-compressed document. The document name is
// authenticated too, so the server can't swap one document for another.
const sealVersion = 1

// ErrDecrypt means a document couldn't be decrypted: another sync code
// encrypted it, or it was damaged or tampered with.
var ErrDecrypt = errors.New("a synced document couldn't be decrypted (was it encrypted with another sync code?)")

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
