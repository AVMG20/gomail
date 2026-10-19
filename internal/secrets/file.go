package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// FileStore keeps secrets in an AES-256-GCM encrypted JSON file. The key is
// a random 32-byte file next to it. Both are created with mode 0600 inside
// the (0700) data directory, so this protects against casual disclosure
// (backups, screenshots of the file, other users) rather than a local
// attacker running as the same user — the same threat model as most
// desktop mail clients without a keyring.
type FileStore struct {
	mu      sync.Mutex
	path    string
	keyPath string
}

func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &FileStore{
		path:    filepath.Join(dir, "secrets.enc"),
		keyPath: filepath.Join(dir, "secrets.key"),
	}, nil
}

func (f *FileStore) key() ([]byte, error) {
	k, err := os.ReadFile(f.keyPath)
	if err == nil && len(k) == 32 {
		return k, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	k = make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(f.keyPath, k); err != nil {
		return nil, err
	}
	return k, nil
}

func (f *FileStore) load() (map[string]string, []byte, error) {
	key, err := f.key()
	if err != nil {
		return nil, nil, fmt.Errorf("secret key: %w", err)
	}
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, key, nil
	}
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, nil, errors.New("secret store is corrupt")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return nil, nil, errors.New("secret store cannot be decrypted (was secrets.key replaced?)")
	}
	m := map[string]string{}
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, nil, err
	}
	return m, key, nil
}

func (f *FileStore) save(m map[string]string, key []byte) error {
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	return writeFileAtomic(f.path, gcm.Seal(nonce, nonce, plain, nil))
}

func (f *FileStore) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *FileStore) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, k, err := f.load()
	if err != nil {
		return err
	}
	m[key] = value
	return f.save(m, k)
}

func (f *FileStore) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := os.Stat(f.path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	m, k, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return f.save(m, k)
}

// writeFileAtomic writes via a temp file + rename so a crash never leaves a
// truncated file behind.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil && !isWindows() {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
