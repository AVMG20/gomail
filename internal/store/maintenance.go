package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// ErrCorrupt means the database file is damaged.
var ErrCorrupt = errors.New("the local database is damaged")

// IsCorrupt reports SQLite corruption errors.
func IsCorrupt(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCorrupt) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "malformed") || strings.Contains(msg, "not a database") || strings.Contains(msg, "corrupt")
}

// QuickCheck runs PRAGMA quick_check; nil means healthy.
func (s *Store) QuickCheck() error {
	rows, err := s.db.Query(`PRAGMA quick_check`)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if len(problems) > 0 {
		if len(problems) > 5 {
			problems = problems[:5]
		}
		return fmt.Errorf("%w: %s", ErrCorrupt, strings.Join(problems, "; "))
	}
	return rows.Err()
}

// Vacuum thresholds (pages of 4 KiB).
const (
	vacuumMinFreePages = 2000
)

// Maintain runs PRAGMA optimize and reclaims free pages: incrementally when
// the database uses auto_vacuum=INCREMENTAL, or with a one-off VACUUM that
// switches it to incremental mode when a quarter of the file is free.
func (s *Store) Maintain() error {
	if _, err := s.db.Exec(`PRAGMA optimize`); err != nil {
		return err
	}
	var autoVacuum, freePages, pages int
	_ = s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum)
	_ = s.db.QueryRow(`PRAGMA freelist_count`).Scan(&freePages)
	_ = s.db.QueryRow(`PRAGMA page_count`).Scan(&pages)
	switch {
	case freePages < vacuumMinFreePages:
		return nil
	case autoVacuum == 2:
		_, err := s.db.Exec(fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, freePages))
		return err
	case freePages > pages/4:
		start := time.Now()
		if _, err := s.db.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			return err
		}
		if _, err := s.db.Exec(`VACUUM`); err != nil {
			return err
		}
		slog.Info("database vacuumed", "freedPages", freePages, "took", time.Since(start))
	}
	return nil
}

// Recover moves a damaged database aside and creates a fresh one, keeping
// what can still be read of accounts and settings (OAuth tokens live in the
// keyring and are unaffected). Mail is downloaded again by the next sync.
func Recover(path string) error {
	var accounts []models.Account
	var signatures []string
	settings := map[string]string{}
	if old, err := sql.Open("sqlite", path+"?mode=ro"); err == nil {
		if rows, err := old.Query(`SELECT id, provider, email, display_name, color, created_at FROM accounts`); err == nil {
			for rows.Next() {
				var a models.Account
				var created int64
				if rows.Scan(&a.ID, &a.Provider, &a.Email, &a.DisplayName, &a.Color, &created) == nil {
					a.CreatedAt = fromMillis(created)
					accounts = append(accounts, a)
				}
			}
			rows.Close()
		}
		for _, a := range accounts {
			var sig string
			_ = old.QueryRow(`SELECT signature FROM accounts WHERE id = ?`, a.ID).Scan(&sig)
			signatures = append(signatures, sig)
		}
		if rows, err := old.Query(`SELECT key, value FROM settings`); err == nil {
			for rows.Next() {
				var k, v string
				if rows.Scan(&k, &v) == nil {
					settings[k] = v
				}
			}
			rows.Close()
		}
		old.Close()
	}
	stamp := time.Now().Format("20060102-150405")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			if err := os.Rename(path+suffix, path+".corrupt-"+stamp+suffix); err != nil {
				return fmt.Errorf("could not move the damaged database aside: %w", err)
			}
		}
	}
	s, err := Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	for i, a := range accounts {
		if err := s.InsertAccount(a); err == nil && signatures[i] != "" {
			_ = s.UpdateSignature(a.ID, signatures[i])
		}
	}
	for k, v := range settings {
		_, _ = s.db.Exec(`INSERT OR REPLACE INTO settings(key, value) VALUES(?, ?)`, k, v)
	}
	slog.Warn("database rebuilt after corruption", "accounts", len(accounts), "backup", path+".corrupt-"+stamp)
	return nil
}
