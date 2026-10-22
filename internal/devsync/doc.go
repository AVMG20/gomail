package devsync

import (
	"bytes"
	"encoding/json"
	"errors"
)

// A synced document is a map of keys to entries, merged per key: the
// entry written last wins (last-writer-wins). A deleted key keeps a
// tombstone, for good, so the deletion spreads to a device that was away
// for a long time too. Timestamps are Unix ms from a hybrid clock: the
// writing device's clock, but never below a timestamp the device has
// already seen (see stamp), so a device whose clock is behind still wins
// with a later change. 0 marks a value a joining device had before it
// first synced, so the synced value wins over it.
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

// merge combines this device's entries with the server's.
func merge(mine, theirs Entries) Entries {
	out := make(Entries, len(mine)+len(theirs))
	for k, e := range theirs {
		out[k] = e
	}
	for k, e := range mine {
		if t, ok := out[k]; !ok || newer(e, t) {
			out[k] = e
		}
	}
	return out
}

// stamp is the timestamp of a change made now: the clock, or just after
// the latest timestamp in the given entries when the clock is behind it.
func stamp(now int64, seen ...Entries) int64 {
	for _, es := range seen {
		for _, e := range es {
			if e.T >= now {
				now = e.T + 1
			}
		}
	}
	return now
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

// compact returns v as compact JSON, so equal values compare equal. No
// value is null (never a deletion).
func compact(v json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(v)) == 0 {
		return json.RawMessage("null")
	}
	var b bytes.Buffer
	if json.Compact(&b, v) != nil {
		return v
	}
	return b.Bytes()
}
