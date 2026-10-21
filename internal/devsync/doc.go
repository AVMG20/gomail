package devsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// A synced document is a map of keys to entries, merged per key: the
// entry written last wins (last-writer-wins). A deleted key keeps a
// tombstone so the deletion spreads; tombstones are forgotten after
// tombstoneTTL. Timestamps are the writing device's clock in Unix ms; 0
// marks a value a device had before it first synced, so the synced value
// wins over it.
type Entry struct {
	V json.RawMessage `json:"v,omitempty"`
	T int64           `json:"t"`
	D bool            `json:"d,omitempty"` // deleted
}

// Entries is a document's content.
type Entries map[string]Entry

const docFormat = "gomail-sync-doc"

type docBody struct {
	Format  string  `json:"format"`
	Version int     `json:"version"`
	Entries Entries `json:"entries"`
}

var tombstoneTTL = 180 * 24 * time.Hour

func (e Entry) equal(o Entry) bool {
	return e.T == o.T && e.D == o.D && bytes.Equal(e.V, o.V)
}

func (es Entries) equal(o Entries) bool {
	if len(es) != len(o) {
		return false
	}
	for k, e := range es {
		if oe, ok := o[k]; !ok || !e.equal(oe) {
			return false
		}
	}
	return true
}

func (es Entries) clone() Entries {
	out := make(Entries, len(es))
	for k, e := range es {
		out[k] = e
	}
	return out
}

// newer reports whether a wins over b. Ties go to b (the server's copy
// when merging), so every device settles on the same value.
func newer(a, b Entry) bool { return a.T > b.T }

// merge combines this device's entries with the server's, dropping
// tombstones older than tombstoneTTL.
func merge(mine, theirs Entries, now time.Time) Entries {
	out := make(Entries, len(mine)+len(theirs))
	for k, e := range theirs {
		out[k] = e
	}
	for k, e := range mine {
		if t, ok := out[k]; !ok || newer(e, t) {
			out[k] = e
		}
	}
	cutoff := now.Add(-tombstoneTTL).UnixMilli()
	for k, e := range out {
		if e.D && e.T < cutoff {
			delete(out, k)
		}
	}
	return out
}

func encodeDoc(es Entries) ([]byte, error) {
	return json.Marshal(docBody{Format: docFormat, Version: 1, Entries: es})
}

func decodeDoc(data []byte) (Entries, error) {
	var b docBody
	if err := json.Unmarshal(data, &b); err != nil || b.Format != docFormat {
		return nil, errors.New("a synced document is damaged")
	}
	if b.Version > 1 {
		return nil, errors.New("another device uses a newer version of gomail; update gomail to keep syncing")
	}
	if b.Entries == nil {
		b.Entries = Entries{}
	}
	return b.Entries, nil
}

// compact returns v as compact JSON, so equal values compare equal.
func compact(v json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if json.Compact(&b, v) != nil {
		return v
	}
	return b.Bytes()
}
