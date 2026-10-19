package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
)

const accountCols = `a.id, a.provider, a.email, a.display_name, a.color, a.created_at, a.last_sync_at, a.sync_error, a.needs_reauth,
	coalesce((SELECT sum(f.unread) FROM folders f WHERE f.account_id = a.id AND f.kind = 'inbox'), 0)`

func scanAccount(sc interface{ Scan(...any) error }) (models.Account, error) {
	var a models.Account
	var created int64
	var last sql.NullInt64
	var reauth int
	err := sc.Scan(&a.ID, &a.Provider, &a.Email, &a.DisplayName, &a.Color, &created, &last, &a.SyncError, &reauth, &a.UnreadCount)
	a.NeedsReauth = reauth == 1
	if err != nil {
		return a, err
	}
	a.CreatedAt = fromMillis(created)
	if last.Valid && last.Int64 > 0 {
		t := fromMillis(last.Int64)
		a.LastSyncAt = &t
	}
	return a, nil
}

// InsertAccount stores a new account.
func (s *Store) InsertAccount(a models.Account) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO accounts(id, provider, email, display_name, color, created_at) VALUES(?,?,?,?,?,?)`,
		a.ID, a.Provider, strings.ToLower(a.Email), a.DisplayName, a.Color, toMillis(a.CreatedAt))
	return err
}

// Accounts lists all accounts in creation order.
func (s *Store) Accounts() ([]models.Account, error) {
	rows, err := s.db.Query(`SELECT ` + accountCols + ` FROM accounts a ORDER BY a.created_at, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Account returns one account.
func (s *Store) Account(id string) (models.Account, error) {
	a, err := scanAccount(s.db.QueryRow(`SELECT `+accountCols+` FROM accounts a WHERE a.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// FindAccount looks an account up by provider and email.
func (s *Store) FindAccount(provider, email string) (models.Account, error) {
	a, err := scanAccount(s.db.QueryRow(`SELECT `+accountCols+` FROM accounts a WHERE a.provider = ? AND a.email = ?`,
		provider, strings.ToLower(email)))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// UpdateAccount changes display name and color.
func (s *Store) UpdateAccount(id, displayName, color string) error {
	res, err := s.db.Exec(`UPDATE accounts SET display_name = ?, color = ? WHERE id = ?`, displayName, color, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSyncResult records the outcome of a sync.
func (s *Store) SetSyncResult(id string, at time.Time, syncErr string) error {
	if syncErr != "" {
		_, err := s.db.Exec(`UPDATE accounts SET sync_error = ? WHERE id = ?`, syncErr, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE accounts SET last_sync_at = ?, sync_error = '' WHERE id = ?`, toMillis(at), id)
	return err
}

// SetNeedsReauth flags (or clears) an account whose sign-in was revoked.
func (s *Store) SetNeedsReauth(id string, needs bool) error {
	_, err := s.db.Exec(`UPDATE accounts SET needs_reauth = ? WHERE id = ?`, boolInt(needs), id)
	return err
}

// DeleteAccount removes the account and (via cascades) all of its data.
func (s *Store) DeleteAccount(id string) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		// Delete messages explicitly first so the FTS trigger runs per row
		// regardless of cascade trigger semantics.
		if _, err := tx.Exec(`DELETE FROM messages WHERE account_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM accounts WHERE id = ?`, id)
		return err
	})
}

// ---- folders ----

var kindOrder = map[string]int{
	models.FolderInbox: 0, models.FolderStarred: 1, models.FolderSent: 2, models.FolderDrafts: 3,
	models.FolderArchive: 4, models.FolderSpam: 5, models.FolderTrash: 6, models.FolderCustom: 7,
}

const folderOrderSQL = `CASE kind WHEN 'inbox' THEN 0 WHEN 'starred' THEN 1 WHEN 'sent' THEN 2 WHEN 'drafts' THEN 3
	WHEN 'archive' THEN 4 WHEN 'spam' THEN 5 WHEN 'trash' THEN 6 ELSE 7 END`

// ReplaceFolders upserts the provider's folder list for an account and
// deletes local folders that no longer exist remotely. Counts for "starred"
// folders are computed locally from the starred flag.
func (s *Store) ReplaceFolders(accountID string, folders []mail.Folder) error {
	return s.tx(context.Background(), func(tx *sql.Tx) error {
		up, err := tx.Prepare(`INSERT INTO folders(id, account_id, remote_id, name, kind, unread, total) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET name = excluded.name, kind = excluded.kind, unread = excluded.unread, total = excluded.total`)
		if err != nil {
			return err
		}
		defer up.Close()
		oldKinds := map[string]string{}
		krows, err := tx.Query(`SELECT id, kind FROM folders WHERE account_id = ?`, accountID)
		if err != nil {
			return err
		}
		for krows.Next() {
			var id, k string
			if err := krows.Scan(&id, &k); err != nil {
				krows.Close()
				return err
			}
			oldKinds[id] = k
		}
		krows.Close()
		changedKinds := false
		keep := map[string]bool{}
		for _, f := range folders {
			if _, ok := kindOrder[f.Kind]; !ok {
				f.Kind = models.FolderCustom
			}
			id := FolderID(accountID, f.RemoteID)
			keep[id] = true
			if k, ok := oldKinds[id]; ok && k != f.Kind {
				changedKinds = true
			}
			if _, err := up.Exec(id, accountID, f.RemoteID, f.Name, f.Kind, f.Unread, f.Total); err != nil {
				return err
			}
		}
		rows, err := tx.Query(`SELECT id FROM folders WHERE account_id = ?`, accountID)
		if err != nil {
			return err
		}
		var stale []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if !keep[id] {
				stale = append(stale, id)
			}
		}
		rows.Close()
		for _, id := range stale {
			if _, err := tx.Exec(`DELETE FROM folders WHERE id = ?`, id); err != nil {
				return err
			}
		}
		if len(stale) > 0 || changedKinds {
			if err := recomputeKinds(tx, "", accountID); err != nil {
				return err
			}
		}
		return recountStarred(tx, accountID)
	})
}

func recountStarred(tx *sql.Tx, accountID string) error {
	_, err := tx.Exec(`UPDATE folders SET
		total  = (SELECT count(*) FROM messages m WHERE m.account_id = folders.account_id AND m.starred = 1),
		unread = (SELECT count(*) FROM messages m WHERE m.account_id = folders.account_id AND m.starred = 1 AND m.unread = 1)
		WHERE account_id = ? AND kind = 'starred'`, accountID)
	return err
}

const folderCols = `id, account_id, remote_id, name, kind, unread, total`

func scanFolder(sc interface{ Scan(...any) error }) (models.Folder, error) {
	var f models.Folder
	err := sc.Scan(&f.ID, &f.AccountID, &f.RemoteID, &f.Name, &f.Kind, &f.Unread, &f.Total)
	return f, err
}

// Folders lists the folders of an account ("" = all accounts), system
// folders first.
func (s *Store) Folders(accountID string) ([]models.Folder, error) {
	q := `SELECT ` + folderCols + ` FROM folders`
	var args []any
	if accountID != "" {
		q += ` WHERE account_id = ?`
		args = append(args, accountID)
	}
	q += ` ORDER BY account_id, ` + folderOrderSQL + `, name COLLATE NOCASE`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Folder{}
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Folder returns one folder by local id.
func (s *Store) Folder(id string) (models.Folder, error) {
	f, err := scanFolder(s.db.QueryRow(`SELECT `+folderCols+` FROM folders WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// FolderByKind returns the first folder of the given kind for an account.
func (s *Store) FolderByKind(accountID, kind string) (models.Folder, error) {
	f, err := scanFolder(s.db.QueryRow(`SELECT `+folderCols+` FROM folders WHERE account_id = ? AND kind = ? ORDER BY name LIMIT 1`, accountID, kind))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}
