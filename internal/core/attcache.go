package core

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// attCache is an on-disk LRU cache of attachment bytes, bounded in size.
// Files live in <data dir>/attachments/<account id>/<hash>; access time is
// tracked through the file modification time.
type attCache struct {
	mu    sync.Mutex
	dir   string
	limit int64
	size  int64 // -1 = unknown
}

const defaultAttCacheLimit = 500 << 20

func newAttCache(dir string, limit int64) *attCache {
	return &attCache{dir: dir, limit: limit, size: -1}
}

func (c *attCache) path(accountID, remoteID, attachmentID string) string {
	h := sha256.Sum256([]byte(remoteID + "\x00" + attachmentID))
	return filepath.Join(c.dir, sanitizeFilename(accountID), hex.EncodeToString(h[:16]))
}

func (c *attCache) get(accountID, remoteID, attachmentID string) ([]byte, bool) {
	if c == nil || c.dir == "" {
		return nil, false
	}
	p := c.path(accountID, remoteID, attachmentID)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	return data, true
}

func (c *attCache) put(accountID, remoteID, attachmentID string, data []byte) {
	if c == nil || c.dir == "" || int64(len(data)) > c.limit/4 {
		return
	}
	p := c.path(accountID, remoteID, attachmentID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size >= 0 {
		c.size += int64(len(data))
	}
	if c.size < 0 || c.size > c.limit {
		c.evictLocked()
	}
}

type cacheFile struct {
	path string
	size int64
	mod  time.Time
}

// evictLocked deletes least recently used files until under 80% of limit.
func (c *attCache) evictLocked() {
	var files []cacheFile
	var total int64
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, cacheFile{p, info.Size(), info.ModTime()})
			total += info.Size()
		}
		return nil
	})
	if total > c.limit {
		sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
		target := c.limit * 8 / 10
		for _, f := range files {
			if total <= target {
				break
			}
			if os.Remove(f.path) == nil {
				total -= f.size
			}
		}
	}
	c.size = total
}

// dropAccount removes an account's cached attachments.
func (c *attCache) dropAccount(accountID string) {
	if c == nil || c.dir == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(c.dir, sanitizeFilename(accountID)))
	c.mu.Lock()
	c.size = -1
	c.mu.Unlock()
}
