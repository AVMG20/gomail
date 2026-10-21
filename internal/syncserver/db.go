package syncserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

var errNotFound = errors.New("not found")

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
	id         TEXT PRIMARY KEY, -- derived from the sync code on the device
	salt       BLOB NOT NULL,
	auth_hash  BLOB NOT NULL, -- sha256(salt || auth key)
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash BLOB PRIMARY KEY, -- sha256(token)
	account    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_account ON sessions(account);
CREATE TABLE IF NOT EXISTS docs (
	account    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	rev        INTEGER NOT NULL,
	data       BLOB NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY(account, name)
);
`

// db is the server's SQLite database.
type db struct{ sql *sql.DB }

func openDB(path string) (*db, error) {
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	sq, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	sq.SetMaxOpenConns(4)
	if _, err := sq.Exec(schema); err != nil {
		sq.Close()
		return nil, err
	}
	return &db{sq}, nil
}

func (d *db) close() error { return d.sql.Close() }

func hashWithSalt(salt, key []byte) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write(key)
	return h.Sum(nil)
}

type createResult int

const (
	accountCreated createResult = iota
	accountExists               // the id is taken
	accountsFull                // the server has max accounts
)

// createAccount stores an account, unless there are max (0 = no limit).
func (d *db) createAccount(id string, salt, authKey []byte, max int) (createResult, error) {
	tx, err := d.sql.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var exists, count int
	if err := tx.QueryRow(`SELECT count(*) FILTER (WHERE id = ?), count(*) FROM accounts`, id).Scan(&exists, &count); err != nil {
		return 0, err
	}
	switch {
	case exists > 0:
		return accountExists, nil
	case max > 0 && count >= max:
		return accountsFull, nil
	}
	if _, err := tx.Exec(`INSERT INTO accounts(id, salt, auth_hash, created_at) VALUES(?,?,?,?)`,
		id, salt, hashWithSalt(salt, authKey), time.Now().UnixMilli()); err != nil {
		return 0, err
	}
	return accountCreated, tx.Commit()
}

// dummySalt keeps a login for an unknown account as slow as a real one.
var dummySalt = make([]byte, 16)

// checkAccount reports whether authKey is the account's key.
func (d *db) checkAccount(id string, authKey []byte) (bool, error) {
	var salt, want []byte
	err := d.sql.QueryRow(`SELECT salt, auth_hash FROM accounts WHERE id = ?`, id).Scan(&salt, &want)
	if errors.Is(err, sql.ErrNoRows) {
		subtle.ConstantTimeCompare(hashWithSalt(dummySalt, authKey), make([]byte, sha256.Size))
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(hashWithSalt(salt, authKey), want) == 1, nil
}

func (d *db) addSession(tokenHash []byte, account string, expires time.Time) error {
	now := time.Now().UnixMilli()
	if _, err := d.sql.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now); err != nil {
		return err
	}
	// Keep an account's newest sessions only (one per device, a few spare).
	if _, err := d.sql.Exec(`DELETE FROM sessions WHERE account = ?1 AND token_hash NOT IN
		(SELECT token_hash FROM sessions WHERE account = ?1 ORDER BY expires_at DESC LIMIT 19)`, account); err != nil {
		return err
	}
	_, err := d.sql.Exec(`INSERT INTO sessions(token_hash, account, expires_at) VALUES(?,?,?)`, tokenHash, account, expires.UnixMilli())
	return err
}

// session returns the account of a valid session.
func (d *db) session(tokenHash []byte) (string, error) {
	var account string
	err := d.sql.QueryRow(`SELECT account FROM sessions WHERE token_hash = ? AND expires_at > ?`, tokenHash, time.Now().UnixMilli()).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound
	}
	return account, err
}

type docRow struct {
	name    string
	rev     int64
	size    int
	updated time.Time
}

func (d *db) listDocs(account string) ([]docRow, error) {
	rows, err := d.sql.Query(`SELECT name, rev, length(data), updated_at FROM docs WHERE account = ? ORDER BY name`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []docRow
	for rows.Next() {
		var r docRow
		var ms int64
		if err := rows.Scan(&r.name, &r.rev, &r.size, &ms); err != nil {
			return nil, err
		}
		r.updated = time.UnixMilli(ms).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *db) getDoc(account, name string) (int64, []byte, error) {
	var rev int64
	var data []byte
	err := d.sql.QueryRow(`SELECT rev, data FROM docs WHERE account = ? AND name = ?`, account, name).Scan(&rev, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, errNotFound
	}
	return rev, data, err
}

// putResult is the outcome of putDoc.
type putResult struct {
	rev      int64 // the new revision, or the current one on a conflict
	conflict bool
	quota    bool
}

// putDoc replaces a document when its revision is baseRev, within the
// account's storage quota and document count.
func (d *db) putDoc(account, name string, baseRev int64, data []byte, quota int64, maxDocs int) (putResult, error) {
	var res putResult
	tx, err := d.sql.BeginTx(context.Background(), nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	var cur int64
	err = tx.QueryRow(`SELECT rev FROM docs WHERE account = ? AND name = ?`, account, name).Scan(&cur)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return res, err
	}
	if cur != baseRev {
		return putResult{rev: cur, conflict: true}, nil
	}
	var used, count int64
	if err := tx.QueryRow(`SELECT coalesce(sum(length(data)), 0), count(*) FROM docs WHERE account = ? AND name != ?`, account, name).Scan(&used, &count); err != nil {
		return res, err
	}
	if used+int64(len(data)) > quota || (!exists && count >= int64(maxDocs)) {
		return putResult{rev: cur, quota: true}, nil
	}
	// Revisions follow the clock (and only grow), so they don't repeat
	// after the database was lost or restored from a backup: a device
	// whose revision matched an old one would miss the new content.
	res.rev = max(cur+1, time.Now().UnixMilli())
	if _, err := tx.Exec(`INSERT INTO docs(account, name, rev, data, updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(account, name) DO UPDATE SET rev = excluded.rev, data = excluded.data, updated_at = excluded.updated_at`,
		account, name, res.rev, data, time.Now().UnixMilli()); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

func (d *db) ping() error { return d.sql.Ping() }
