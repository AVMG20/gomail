// Package store is the local SQLite cache: accounts, folders, message
// summaries and bodies, attachments metadata, settings and sync state, with
// an FTS5 index for search. It uses the pure-Go modernc.org/sqlite driver so
// builds need no cgo.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a row doesn't exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	db           *sql.DB
	labels       labelIndex
	hideProvider atomic.Bool

	vocabMu sync.Mutex
	vocab   *vocab // lazily loaded spelling vocabulary

	// gen increments on every committed write transaction; it invalidates
	// cached list totals (infinite scroll re-asks for the same count).
	gen        atomic.Int64
	writes     sync.WaitGroup // in-flight write transactions
	countMu    sync.Mutex
	countCache map[string]countEntry
}

type countEntry struct {
	gen   int64
	count int
}

// cachedCount runs a COUNT query, reusing the result until the next write.
func (s *Store) cachedCount(q string, args []any) (int, error) {
	key := q + "\x00" + fmt.Sprint(args...)
	gen := s.gen.Load()
	s.countMu.Lock()
	if e, ok := s.countCache[key]; ok && e.gen == gen {
		s.countMu.Unlock()
		return e.count, nil
	}
	s.countMu.Unlock()
	var n int
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, err
	}
	s.countMu.Lock()
	if s.countCache == nil || len(s.countCache) > 64 {
		s.countCache = map[string]countEntry{}
	}
	s.countCache[key] = countEntry{gen, n}
	s.countMu.Unlock()
	return n, nil
}

// SetHideProviderLabels hides (true) or shows provider-sourced label
// assignments in every read path. The data is kept and kept in sync either
// way, so toggling is instant.
func (s *Store) SetHideProviderLabels(hide bool) { s.hideProvider.Store(hide) }

// labelCond returns an extra condition on the "ml" alias for visible labels.
func (s *Store) labelCond() string {
	if s.hideProvider.Load() {
		return ` AND ml.source != 'provider'`
	}
	return ""
}

func (s *Store) summaryCols() string {
	return `m.id, m.account_id, m.remote_id, m.thread_id, m.from_name, m.from_email, m.to_json, m.subject, m.snippet,
	m.date, m.unread, m.starred, m.has_attachments,
	coalesce((SELECT group_concat(mf.folder_id) FROM message_folders mf WHERE mf.message_id = m.id), ''),
	coalesce((SELECT group_concat(ml.label_id || '|' || ml.source || '|' || ml.confidence) FROM message_labels ml WHERE ml.message_id = m.id` + s.labelCond() + `), '')`
}

// Open opens (creating if needed) the database at path and runs migrations.
// Use ":memory:" for an ephemeral database (tests).
func Open(path string) (*Store, error) {
	q := url.Values{}
	// auto_vacuum only takes effect on a new database (before the first
	// table); existing files are switched by Maintain's one-off VACUUM.
	for _, p := range []string{"auto_vacuum(INCREMENTAL)", "journal_mode(WAL)", "busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)", "temp_store(MEMORY)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	dsn := path + "?" + q.Encode()
	if path == ":memory:" {
		// A shared in-memory db must stay on one connection.
		dsn = "file::memory:?" + q.Encode()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(8)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		if IsCorrupt(err) {
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Store{db: db}

	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		if IsCorrupt(err) {
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return s, nil
}

// Close waits (up to 5 s) for in-flight write transactions, then closes.
func (s *Store) Close() error {
	done := make(chan struct{})
	go func() { s.writes.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("closing the database with writes still in flight")
	}
	_, _ = s.db.Exec(`PRAGMA optimize`)
	return s.db.Close()
}

// DB exposes the handle for tests.
func (s *Store) DB() *sql.DB { return s.db }

func hashID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return prefix + hex.EncodeToString(h.Sum(nil)[:10])
}

// MessageID returns the deterministic local id for a remote message.
func MessageID(accountID, remoteID string) string { return hashID("m", accountID, remoteID) }

// FolderID returns the deterministic local id for a remote folder.
func FolderID(accountID, remoteID string) string { return hashID("f", accountID, remoteID) }

func toMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// tx runs f in a transaction.
func (s *Store) tx(ctx context.Context, f func(*sql.Tx) error) error {
	s.writes.Add(1)
	defer s.writes.Done()
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(t); err != nil {
		_ = t.Rollback()
		return err
	}
	err = t.Commit()
	s.gen.Add(1)
	return err
}

// ---- settings ----

// GetSetting decodes the JSON value stored under key into v. It returns
// ErrNotFound if the key is absent.
func (s *Store) GetSetting(key string, v any) error {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

// RawSetting returns the stored JSON of a setting.
func (s *Store) RawSetting(key string) ([]byte, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return []byte(raw), err
}

// PutRawSetting stores data as is (e.g. a copy of a damaged setting).
func (s *Store) PutRawSetting(key string, data []byte) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(data))
	return err
}

// SetSetting stores v as JSON under key.
func (s *Store) SetSetting(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(b))
	return err
}

// ---- sync state ----

// SyncState returns the provider sync state of an account.
func (s *Store) SyncState(accountID string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM sync_state WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// ClearSyncState forgets the provider sync state (forces a full resync).
func (s *Store) ClearSyncState(accountID string) error {
	_, err := s.db.Exec(`DELETE FROM sync_state WHERE account_id = ?`, accountID)
	return err
}

func saveSyncState(tx *sql.Tx, accountID string, state map[string]string) error {
	for k, v := range state {
		var err error
		if v == "" {
			_, err = tx.Exec(`DELETE FROM sync_state WHERE account_id = ? AND key = ?`, accountID, k)
		} else {
			_, err = tx.Exec(`INSERT INTO sync_state(account_id, key, value) VALUES(?, ?, ?)
				ON CONFLICT(account_id, key) DO UPDATE SET value = excluded.value`, accountID, k, v)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// kindBits maps folder kinds to the bits of messages.folder_kinds. Starred
// is a flag, not a folder membership.
var kindBits = map[string]int{
	"inbox": 1, "sent": 2, "drafts": 4, "archive": 8, "spam": 16, "trash": 32, "custom": 64,
}

func kindMask(kinds []string) int {
	m := 0
	for _, k := range kinds {
		m |= kindBits[k]
	}
	return m
}

const kindBitSQL = `CASE f.kind WHEN 'inbox' THEN 1 WHEN 'sent' THEN 2 WHEN 'drafts' THEN 4 WHEN 'archive' THEN 8
	WHEN 'spam' THEN 16 WHEN 'trash' THEN 32 WHEN 'custom' THEN 64 ELSE 0 END`

const recomputeKindsSQL = `UPDATE messages SET folder_kinds = coalesce((SELECT sum(DISTINCT ` + kindBitSQL + `)
		FROM message_folders mf JOIN folders f ON f.id = mf.folder_id WHERE mf.message_id = messages.id), 0)`

// kindsUpdater prepares the per-message folder_kinds refresh once per tx.
type kindsUpdater struct{ st *sql.Stmt }

func newKindsUpdater(tx *sql.Tx) (*kindsUpdater, error) {
	st, err := tx.Prepare(recomputeKindsSQL + ` WHERE id = ?`)
	return &kindsUpdater{st}, err
}

func (k *kindsUpdater) update(id string) error { _, err := k.st.Exec(id); return err }
func (k *kindsUpdater) close()                 { k.st.Close() }

// recomputeKinds refreshes messages.folder_kinds for one message ("" + accountID = all of an account).
func recomputeKinds(tx *sql.Tx, msgID, accountID string) error {
	q := recomputeKindsSQL
	var err error
	if msgID != "" {
		_, err = tx.Exec(q+` WHERE id = ?`, msgID)
	} else {
		_, err = tx.Exec(q+` WHERE account_id = ?`, accountID)
	}
	return err
}
