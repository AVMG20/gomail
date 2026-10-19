package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations are applied in order; PRAGMA user_version records how many ran.
var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE accounts (
	id           TEXT PRIMARY KEY,
	provider     TEXT NOT NULL,
	email        TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	color        TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	last_sync_at INTEGER,
	sync_error   TEXT NOT NULL DEFAULT '',
	UNIQUE(provider, email)
);

CREATE TABLE folders (
	id         TEXT PRIMARY KEY,
	account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	remote_id  TEXT NOT NULL,
	name       TEXT NOT NULL,
	kind       TEXT NOT NULL,
	unread     INTEGER NOT NULL DEFAULT 0,
	total      INTEGER NOT NULL DEFAULT 0,
	UNIQUE(account_id, remote_id)
);
CREATE INDEX folders_account_kind ON folders(account_id, kind);

CREATE TABLE messages (
	rid             INTEGER PRIMARY KEY, -- stable rowid for the contentless FTS index
	id              TEXT NOT NULL UNIQUE,
	account_id      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	remote_id       TEXT NOT NULL,
	thread_id       TEXT NOT NULL DEFAULT '',
	from_name       TEXT NOT NULL DEFAULT '',
	from_email      TEXT NOT NULL DEFAULT '',
	to_json         TEXT NOT NULL DEFAULT '[]',
	cc_json         TEXT NOT NULL DEFAULT '[]',
	bcc_json        TEXT NOT NULL DEFAULT '[]',
	reply_to_json   TEXT NOT NULL DEFAULT '[]',
	subject         TEXT NOT NULL DEFAULT '',
	snippet         TEXT NOT NULL DEFAULT '',
	date            INTEGER NOT NULL DEFAULT 0,
	unread          INTEGER NOT NULL DEFAULT 0,
	starred         INTEGER NOT NULL DEFAULT 0,
	has_attachments INTEGER NOT NULL DEFAULT 0,
	category        TEXT NOT NULL DEFAULT '',
	category_source TEXT NOT NULL DEFAULT '',
	category_conf   REAL NOT NULL DEFAULT 0,
	classified_at   INTEGER NOT NULL DEFAULT 0,
	message_id_hdr  TEXT NOT NULL DEFAULT '',
	in_reply_to     TEXT NOT NULL DEFAULT '',
	refs            TEXT NOT NULL DEFAULT '',
	body_html       TEXT,
	body_text       TEXT,
	has_remote      INTEGER NOT NULL DEFAULT 0,
	body_fetched_at INTEGER NOT NULL DEFAULT 0,
	updated_at      INTEGER NOT NULL DEFAULT 0,
	UNIQUE(account_id, remote_id)
);
CREATE INDEX messages_date ON messages(date DESC, id DESC);
CREATE INDEX messages_account_date ON messages(account_id, date DESC, id DESC);
CREATE INDEX messages_thread ON messages(account_id, thread_id, date);
CREATE INDEX messages_category_date ON messages(category, date DESC, id DESC);
CREATE INDEX messages_unread_date ON messages(unread, date DESC, id DESC) WHERE unread = 1;

CREATE TABLE message_folders (
	folder_id  TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	PRIMARY KEY(folder_id, message_id)
) WITHOUT ROWID;
CREATE INDEX message_folders_message ON message_folders(message_id, folder_id);

CREATE TABLE attachments (
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	id         TEXT NOT NULL,
	filename   TEXT NOT NULL DEFAULT '',
	mime_type  TEXT NOT NULL DEFAULT '',
	size       INTEGER NOT NULL DEFAULT 0,
	inline     INTEGER NOT NULL DEFAULT 0,
	content_id TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(message_id, id)
) WITHOUT ROWID;

CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE sync_state (
	account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	key        TEXT NOT NULL,
	value      TEXT NOT NULL,
	PRIMARY KEY(account_id, key)
);

CREATE VIRTUAL TABLE messages_fts USING fts5(
	subject, sender, recipients, snippet, body,
	content='', contentless_delete=1,
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER messages_fts_ai AFTER INSERT ON messages BEGIN
	INSERT INTO messages_fts(rowid, subject, sender, recipients, snippet, body)
	VALUES (new.rid, new.subject, new.from_name || ' ' || new.from_email,
		(SELECT coalesce(group_concat(coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.email'), ''), ' '), '')
		 FROM json_each(new.to_json ) ) || ' ' ||
		(SELECT coalesce(group_concat(coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.email'), ''), ' '), '')
		 FROM json_each(new.cc_json ) ),
		new.snippet, coalesce(new.body_text, ''));
END;

CREATE TRIGGER messages_fts_ad AFTER DELETE ON messages BEGIN
	DELETE FROM messages_fts WHERE rowid = old.rid;
END;

CREATE TRIGGER messages_fts_au AFTER UPDATE OF subject, from_name, from_email, to_json, cc_json, snippet, body_text ON messages BEGIN
	DELETE FROM messages_fts WHERE rowid = old.rid;
	INSERT INTO messages_fts(rowid, subject, sender, recipients, snippet, body)
	VALUES (new.rid, new.subject, new.from_name || ' ' || new.from_email,
		(SELECT coalesce(group_concat(coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.email'), ''), ' '), '')
		 FROM json_each(new.to_json ) ) || ' ' ||
		(SELECT coalesce(group_concat(coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.email'), ''), ' '), '')
		 FROM json_each(new.cc_json ) ),
		new.snippet, coalesce(new.body_text, ''));
END;
`,
}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d); please update gomail", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		err := s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, i+1))
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
