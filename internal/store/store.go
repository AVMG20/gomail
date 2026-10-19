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
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a row doesn't exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and runs migrations.
// Use ":memory:" for an ephemeral database (tests).
func Open(path string) (*Store, error) {
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)", "temp_store(MEMORY)"} {
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
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
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
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(t); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
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
