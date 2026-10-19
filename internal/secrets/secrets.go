// Package secrets stores OAuth tokens and API keys. It prefers the OS
// credential store (macOS Keychain, Windows Credential Manager, Secret
// Service on Linux) and falls back to an AES-GCM encrypted file in the data
// directory when no keyring is available (e.g. headless Linux) or a value is
// too large for it (Windows limits credentials to 2.5 KB).
package secrets

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/AVMG20/gomail/internal/safe"
)

// ErrNotFound is returned when no secret exists for a key.
var ErrNotFound = errors.New("secret not found")

// Store is a small key/value store for sensitive strings.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// EnvBackend forces a backend: "file" or "keyring".
const EnvBackend = "GOMAIL_SECRET_BACKEND"

const service = "gomail"

// New returns the default store for dataDir: keyring with an encrypted-file
// fallback. GOMAIL_SECRET_BACKEND=file disables the keyring.
func New(dataDir string) (Store, error) {
	file, err := NewFileStore(dataDir)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(os.Getenv(EnvBackend), "file") {
		return file, nil
	}
	return &autoStore{kr: keyringStore{service: service}, file: file}, nil
}

// keyringStore wraps go-keyring with a timeout, because Secret Service calls
// over D-Bus can block indefinitely when no agent answers.
type keyringStore struct{ service string }

const keyringTimeout = 5 * time.Second

var errKeyringTimeout = errors.New("keyring timed out")

func withTimeout[T any](f func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		defer safe.Recover("keyring")
		v, err := f()
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(keyringTimeout):
		var zero T
		return zero, errKeyringTimeout
	}
}

// chunkSize keeps every keyring entry below Windows Credential Manager's
// 2560-byte blob limit (Microsoft refresh tokens can be longer); larger
// values are split over "<key>#<n>" entries behind a header entry.
var chunkSize = 2000

const chunkHeader = "gomail-chunks:"

func (k keyringStore) raw(key string) (string, error) {
	v, err := withTimeout(func() (string, error) { return keyring.Get(k.service, key) })
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (k keyringStore) put(key, value string) error {
	_, err := withTimeout(func() (struct{}, error) { return struct{}{}, keyring.Set(k.service, key, value) })
	return err
}

func (k keyringStore) del(key string) error {
	_, err := withTimeout(func() (struct{}, error) { return struct{}{}, keyring.Delete(k.service, key) })
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

func chunkKey(key string, i int) string { return key + "#" + strconv.Itoa(i) }

func (k keyringStore) Get(key string) (string, error) {
	v, err := k.raw(key)
	if err != nil || !strings.HasPrefix(v, chunkHeader) {
		return v, err
	}
	n, err := strconv.Atoi(strings.TrimPrefix(v, chunkHeader))
	if err != nil || n <= 0 || n > 1000 {
		return "", errors.New("keyring entry is damaged")
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		part, err := k.raw(chunkKey(key, i))
		if err != nil {
			return "", fmt.Errorf("keyring entry is incomplete: %w", err)
		}
		b.WriteString(part)
	}
	return b.String(), nil
}

// chunks returns the number of chunk entries currently stored for key.
func (k keyringStore) chunks(key string) int {
	v, err := k.raw(key)
	if err != nil || !strings.HasPrefix(v, chunkHeader) {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(v, chunkHeader))
	return n
}

func (k keyringStore) Set(key, value string) error {
	old := k.chunks(key)
	if len(value) <= chunkSize && !strings.HasPrefix(value, chunkHeader) {
		if err := k.put(key, value); err != nil {
			return err
		}
	} else {
		n := 0
		for off := 0; off < len(value); off += chunkSize {
			if err := k.put(chunkKey(key, n), value[off:min(off+chunkSize, len(value))]); err != nil {
				return err
			}
			n++
		}
		if err := k.put(key, chunkHeader+strconv.Itoa(n)); err != nil {
			return err
		}
		if old > n {
			for i := n; i < old; i++ {
				_ = k.del(chunkKey(key, i))
			}
		}
		return nil
	}
	for i := 0; i < old; i++ {
		_ = k.del(chunkKey(key, i))
	}
	return nil
}

func (k keyringStore) Delete(key string) error {
	for i := 0; i < k.chunks(key); i++ {
		_ = k.del(chunkKey(key, i))
	}
	return k.del(key)
}

// autoStore tries the keyring first and transparently falls back to the
// encrypted file. Once the keyring fails with an infrastructure error it is
// disabled for the rest of the process to avoid repeated timeouts.
type autoStore struct {
	kr       keyringStore
	file     *FileStore
	mu       sync.Mutex
	disabled bool
}

func (a *autoStore) keyringOK() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.disabled
}

func (a *autoStore) keyringFailed(op string, err error) {
	if errors.Is(err, keyring.ErrSetDataTooBig) {
		return // value-specific, keyring itself works
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.disabled {
		slog.Warn("OS keyring unavailable, using encrypted file store", "op", op, "err", err)
	}
	a.disabled = true
}

func (a *autoStore) Get(key string) (string, error) {
	if a.keyringOK() {
		v, err := a.kr.Get(key)
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, ErrNotFound) {
			a.keyringFailed("get", err)
		}
	}
	return a.file.Get(key)
}

func (a *autoStore) Set(key, value string) error {
	if a.keyringOK() {
		err := a.kr.Set(key, value)
		if err == nil {
			_ = a.file.Delete(key) // drop any stale fallback copy
			return nil
		}
		a.keyringFailed("set", err)
		_ = a.kr.Delete(key) // never leave a stale value that Get would prefer
	}
	return a.file.Set(key, value)
}

func (a *autoStore) Delete(key string) error {
	var kerr error
	if a.keyringOK() {
		if kerr = a.kr.Delete(key); kerr != nil {
			a.keyringFailed("delete", kerr)
		}
	}
	return a.file.Delete(key)
}

// Memory is an in-memory Store for tests and demo mode.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (m *Memory) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Memory) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[key] = value
	return nil
}

func (m *Memory) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, key)
	return nil
}
