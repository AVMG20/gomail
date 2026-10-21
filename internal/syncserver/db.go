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
CREATE TABLE IF NOT EXISTS users (
	username   TEXT PRIMARY KEY,
	salt       BLOB NOT NULL,
	auth_hash  BLOB NOT NULL, -- sha256(salt || auth key)
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash BLOB PRIMARY KEY, -- sha256(token)
	username   TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(username);
CREATE TABLE IF NOT EXISTS docs (
	username   TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	rev        INTEGER NOT NULL,
	data       BLOB NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY(username, name)
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

// createUser stores a user; false when the name is taken.
func (d *db) createUser(username string, salt, authKey []byte) (bool, error) {
	res, err := d.sql.Exec(`INSERT INTO users(username, salt, auth_hash, created_at) VALUES(?,?,?,?) ON CONFLICT(username) DO NOTHING`,
		username, salt, hashWithSalt(salt, authKey), time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// dummySalt keeps a login for an unknown user as slow as a real one.
var dummySalt = make([]byte, 16)

// checkUser reports whether authKey is the user's key.
func (d *db) checkUser(username string, authKey []byte) (bool, error) {
	var salt, want []byte
	err := d.sql.QueryRow(`SELECT salt, auth_hash FROM users WHERE username = ?`, username).Scan(&salt, &want)
	if errors.Is(err, sql.ErrNoRows) {
		subtle.ConstantTimeCompare(hashWithSalt(dummySalt, authKey), make([]byte, sha256.Size))
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(hashWithSalt(salt, authKey), want) == 1, nil
}

func (d *db) addSession(tokenHash []byte, username string, expires time.Time) error {
	now := time.Now().UnixMilli()
	if _, err := d.sql.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now); err != nil {
		return err
	}
	// Keep the newest sessions of a user only (one per device, a few spare).
	if _, err := d.sql.Exec(`DELETE FROM sessions WHERE username = ?1 AND token_hash NOT IN
		(SELECT token_hash FROM sessions WHERE username = ?1 ORDER BY expires_at DESC LIMIT 19)`, username); err != nil {
		return err
	}
	_, err := d.sql.Exec(`INSERT INTO sessions(token_hash, username, expires_at) VALUES(?,?,?)`, tokenHash, username, expires.UnixMilli())
	return err
}

// session returns the user of a valid session.
func (d *db) session(tokenHash []byte) (string, error) {
	var user string
	err := d.sql.QueryRow(`SELECT username FROM sessions WHERE token_hash = ? AND expires_at > ?`, tokenHash, time.Now().UnixMilli()).Scan(&user)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound
	}
	return user, err
}

type docRow struct {
	name    string
	rev     int64
	size    int
	updated time.Time
}

func (d *db) listDocs(username string) ([]docRow, error) {
	rows, err := d.sql.Query(`SELECT name, rev, length(data), updated_at FROM docs WHERE username = ? ORDER BY name`, username)
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

func (d *db) getDoc(username, name string) (int64, []byte, error) {
	var rev int64
	var data []byte
	err := d.sql.QueryRow(`SELECT rev, data FROM docs WHERE username = ? AND name = ?`, username, name).Scan(&rev, &data)
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
func (d *db) putDoc(username, name string, baseRev int64, data []byte, quota int64, maxDocs int) (putResult, error) {
	var res putResult
	ctx := context.Background()
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	var cur int64
	err = tx.QueryRow(`SELECT rev FROM docs WHERE username = ? AND name = ?`, username, name).Scan(&cur)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return res, err
	}
	if cur != baseRev {
		return putResult{rev: cur, conflict: true}, nil
	}
	var used, count int64
	if err := tx.QueryRow(`SELECT coalesce(sum(length(data)), 0), count(*) FROM docs WHERE username = ? AND name != ?`, username, name).Scan(&used, &count); err != nil {
		return res, err
	}
	if used+int64(len(data)) > quota || (!exists && count >= int64(maxDocs)) {
		return putResult{rev: cur, quota: true}, nil
	}
	res.rev = cur + 1
	if _, err := tx.Exec(`INSERT INTO docs(username, name, rev, data, updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(username, name) DO UPDATE SET rev = excluded.rev, data = excluded.data, updated_at = excluded.updated_at`,
		username, name, res.rev, data, time.Now().UnixMilli()); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

func (d *db) ping() error { return d.sql.Ping() }
