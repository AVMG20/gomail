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
	classified_at   INTEGER NOT NULL DEFAULT 0,
	message_id_hdr  TEXT NOT NULL DEFAULT '',
	in_reply_to     TEXT NOT NULL DEFAULT '',
	refs            TEXT NOT NULL DEFAULT '',
	body_html       TEXT,
	body_text       TEXT,
	has_remote      INTEGER NOT NULL DEFAULT 0,
	folder_kinds    INTEGER NOT NULL DEFAULT 0, -- bitmask of the kinds of folders containing the message (see kindBits)
	body_fetched_at INTEGER NOT NULL DEFAULT 0,
	updated_at      INTEGER NOT NULL DEFAULT 0,
	UNIQUE(account_id, remote_id)
);
CREATE INDEX messages_date ON messages(date DESC, id DESC);
CREATE INDEX messages_account_date ON messages(account_id, date DESC, id DESC);
CREATE INDEX messages_thread ON messages(account_id, thread_id, date);
CREATE INDEX messages_account_kinds ON messages(account_id, folder_kinds);
CREATE INDEX messages_unread_cover ON messages(unread, account_id, folder_kinds);
CREATE INDEX messages_unread_date ON messages(unread, date DESC, id DESC) WHERE unread = 1;

CREATE TABLE message_folders (
	folder_id  TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	PRIMARY KEY(folder_id, message_id)
) WITHOUT ROWID;
CREATE INDEX message_folders_message ON message_folders(message_id, folder_id);

-- Label assignments: source is 'ai' or 'manual'.
CREATE TABLE message_labels (
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	label_id   TEXT NOT NULL,
	source     TEXT NOT NULL,
	confidence REAL NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(message_id, label_id)
) WITHOUT ROWID;
CREATE INDEX message_labels_label ON message_labels(label_id, message_id);

-- AI corrections of provider/system labels: while a row exists, the next
-- sync doesn't re-add that provider assignment. Rows are dropped once the
-- provider stops reporting the label (so a later re-add counts again).
CREATE TABLE label_overrides (
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	label_id   TEXT NOT NULL,
	created_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(message_id, label_id)
) WITHOUT ROWID;

CREATE TABLE attachments (
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	id         TEXT NOT NULL,
	filename   TEXT NOT NULL DEFAULT '',
	mime_type  TEXT NOT NULL DEFAULT '',
	size       INTEGER NOT NULL DEFAULT 0,
	inline     INTEGER NOT NULL DEFAULT 0,
	content_id TEXT NOT NULL DEFAULT '',
	pos        INTEGER NOT NULL DEFAULT 0,
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

INSERT INTO messages_fts(messages_fts, rank) VALUES('rank', 'bm25(10.0, 5.0, 3.0, 2.0, 1.0)');

-- Words seen in subjects and senders, for spelling correction in search.
CREATE TABLE vocab (term TEXT PRIMARY KEY) WITHOUT ROWID;

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
	// 2: conversations, List-Unsubscribe, re-auth state
	`
ALTER TABLE messages ADD COLUMN thread_key TEXT GENERATED ALWAYS AS (CASE WHEN thread_id = '' THEN id ELSE thread_id END) VIRTUAL;
CREATE INDEX messages_thread_key ON messages(account_id, thread_key, date DESC, id DESC);
ALTER TABLE messages ADD COLUMN unsub_url TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN unsub_mailto TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN unsub_one_click INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN needs_reauth INTEGER NOT NULL DEFAULT 0;
`,
	// 3: signatures, contacts, faster conversation and label counts
	`
ALTER TABLE accounts ADD COLUMN signature TEXT NOT NULL DEFAULT '';

-- Conversation counts: covering index over the filter columns + thread.
CREATE INDEX messages_kinds_thread ON messages(account_id, folder_kinds, thread_key);

-- Label counts: denormalized message state on assignments (trigger-maintained).
ALTER TABLE message_labels ADD COLUMN account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE message_labels ADD COLUMN unread INTEGER NOT NULL DEFAULT 0;
ALTER TABLE message_labels ADD COLUMN trashed INTEGER NOT NULL DEFAULT 0;
UPDATE message_labels SET (account_id, unread, trashed) =
	(SELECT m.account_id, m.unread, (m.folder_kinds & 32) != 0 FROM messages m WHERE m.id = message_labels.message_id);
CREATE INDEX message_labels_counts ON message_labels(trashed, label_id, account_id, unread, source);
CREATE TRIGGER message_labels_fill AFTER INSERT ON message_labels BEGIN
	UPDATE message_labels SET (account_id, unread, trashed) =
		(SELECT m.account_id, m.unread, (m.folder_kinds & 32) != 0 FROM messages m WHERE m.id = new.message_id)
	WHERE message_id = new.message_id AND label_id = new.label_id;
END;
CREATE TRIGGER messages_label_state AFTER UPDATE OF unread, folder_kinds ON messages
WHEN old.unread != new.unread OR ((old.folder_kinds & 32) != 0) != ((new.folder_kinds & 32) != 0) BEGIN
	UPDATE message_labels SET unread = new.unread, trashed = (new.folder_kinds & 32) != 0 WHERE message_id = new.id;
END;

-- Only re-index full text when indexed columns really change.
DROP TRIGGER messages_fts_au;
CREATE TRIGGER messages_fts_au AFTER UPDATE OF subject, from_name, from_email, to_json, cc_json, snippet, body_text ON messages
WHEN old.subject IS NOT new.subject OR old.from_name IS NOT new.from_name OR old.from_email IS NOT new.from_email
	OR old.to_json IS NOT new.to_json OR old.cc_json IS NOT new.cc_json OR old.snippet IS NOT new.snippet OR old.body_text IS NOT new.body_text
BEGIN
	DELETE FROM messages_fts WHERE rowid = old.rid;
	INSERT INTO messages_fts(rowid, subject, sender, recipients, snippet, body)
	VALUES (new.rid, new.subject, new.from_name || ' ' || new.from_email,
		(SELECT coalesce(group_concat(coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.email'), ''), ' '), '')
		 FROM json_each(new.to_json)) || ' ' ||
		(SELECT coalesce(group_concat(coalesce(json_extract(value, '$.name'), '') || ' ' || coalesce(json_extract(value, '$.email'), ''), ' '), '')
		 FROM json_each(new.cc_json)),
		new.snippet, coalesce(new.body_text, ''));
END;

-- Address book for recipient autocomplete (weights: received 1, sent-to 5).
CREATE TABLE contacts (
	email     TEXT PRIMARY KEY, -- lower-cased
	name      TEXT NOT NULL DEFAULT '',
	count     INTEGER NOT NULL DEFAULT 0,
	last_seen INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
INSERT INTO contacts(email, name, count, last_seen)
	SELECT lower(from_email), max(from_name), count(*), max(date) FROM messages WHERE from_email != '' GROUP BY lower(from_email)
	ON CONFLICT(email) DO NOTHING;
INSERT INTO contacts(email, name, count, last_seen)
	SELECT lower(json_extract(j.value, '$.email')), max(coalesce(json_extract(j.value, '$.name'), '')), count(*), max(m.date)
	FROM messages m, json_each(m.to_json) j WHERE coalesce(json_extract(j.value, '$.email'), '') != '' GROUP BY 1
	ON CONFLICT(email) DO UPDATE SET count = count + excluded.count, last_seen = max(last_seen, excluded.last_seen),
		name = CASE WHEN name = '' THEN excluded.name ELSE name END;
`,
	// 4: maintained conversation counters, AI usage, AI re-run hashes
	`
CREATE TABLE folder_threads (
	folder_id  TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
	thread_key TEXT NOT NULL,
	msgs       INTEGER NOT NULL DEFAULT 0, -- messages of the thread in the folder
	unread     INTEGER NOT NULL DEFAULT 0, -- of which unread
	PRIMARY KEY(folder_id, thread_key)
) WITHOUT ROWID;
CREATE INDEX folder_threads_unread ON folder_threads(folder_id, unread);
INSERT INTO folder_threads(folder_id, thread_key, msgs, unread)
	SELECT mf.folder_id, m.thread_key, count(*), sum(m.unread)
	FROM message_folders mf JOIN messages m ON m.id = mf.message_id GROUP BY mf.folder_id, m.thread_key;

CREATE TRIGGER folder_threads_add AFTER INSERT ON message_folders BEGIN
	INSERT INTO folder_threads(folder_id, thread_key, msgs, unread)
		SELECT new.folder_id, m.thread_key, 1, m.unread FROM messages m WHERE m.id = new.message_id
		ON CONFLICT(folder_id, thread_key) DO UPDATE SET msgs = msgs + 1, unread = unread + excluded.unread;
END;
CREATE TRIGGER folder_threads_remove AFTER DELETE ON message_folders BEGIN
	UPDATE folder_threads SET msgs = msgs - 1,
		unread = unread - coalesce((SELECT m.unread FROM messages m WHERE m.id = old.message_id), 0)
	WHERE folder_id = old.folder_id AND thread_key = (SELECT m.thread_key FROM messages m WHERE m.id = old.message_id);
	DELETE FROM folder_threads WHERE folder_id = old.folder_id AND msgs <= 0;
END;
-- Remove memberships while the message row still exists, so the counters
-- above can see its thread and unread state.
CREATE TRIGGER messages_before_delete BEFORE DELETE ON messages BEGIN
	DELETE FROM message_folders WHERE message_id = old.id;
END;
CREATE TRIGGER folder_threads_unread AFTER UPDATE OF unread ON messages WHEN old.unread != new.unread BEGIN
	UPDATE folder_threads SET unread = unread + (new.unread - old.unread)
	WHERE thread_key = new.thread_key AND folder_id IN (SELECT folder_id FROM message_folders WHERE message_id = new.id);
END;
CREATE TRIGGER folder_threads_rethread AFTER UPDATE OF thread_id ON messages WHEN old.thread_id != new.thread_id BEGIN
	UPDATE folder_threads SET msgs = msgs - 1, unread = unread - old.unread
	WHERE thread_key = (CASE WHEN old.thread_id = '' THEN old.id ELSE old.thread_id END)
		AND folder_id IN (SELECT folder_id FROM message_folders WHERE message_id = new.id);
	DELETE FROM folder_threads WHERE msgs <= 0 AND thread_key = (CASE WHEN old.thread_id = '' THEN old.id ELSE old.thread_id END);
	INSERT INTO folder_threads(folder_id, thread_key, msgs, unread)
		SELECT mf.folder_id, new.thread_key, 1, new.unread FROM message_folders mf WHERE mf.message_id = new.id
		ON CONFLICT(folder_id, thread_key) DO UPDATE SET msgs = msgs + 1, unread = unread + excluded.unread;
END;

CREATE TABLE ai_usage (
	at            INTEGER NOT NULL, -- unix ms
	kind          TEXT NOT NULL,    -- "decision" | "chat"
	model         TEXT NOT NULL DEFAULT '',
	cost          REAL NOT NULL DEFAULT 0,
	input_tokens  INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX ai_usage_at ON ai_usage(at);

CREATE INDEX messages_account_starred ON messages(account_id, starred, unread);

ALTER TABLE messages ADD COLUMN ai_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN ai_scores TEXT NOT NULL DEFAULT '';
`,
	// 5: calendars
	`
ALTER TABLE accounts ADD COLUMN calendar_consent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN calendar_enabled INTEGER NOT NULL DEFAULT 1;

CREATE TABLE calendars (
	id           TEXT PRIMARY KEY,
	account_id   TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	remote_id    TEXT NOT NULL,
	name         TEXT NOT NULL DEFAULT '',
	color        TEXT NOT NULL DEFAULT '',
	is_primary   INTEGER NOT NULL DEFAULT 0,
	can_edit     INTEGER NOT NULL DEFAULT 0,
	visible      INTEGER NOT NULL DEFAULT 1,
	time_zone    TEXT NOT NULL DEFAULT '',
	reminders    TEXT NOT NULL DEFAULT '', -- JSON default reminder minutes
	sync_state   TEXT NOT NULL DEFAULT '', -- provider sync token / delta link
	window_start INTEGER NOT NULL DEFAULT 0, -- unix ms of the synced range
	window_end   INTEGER NOT NULL DEFAULT 0,
	UNIQUE(account_id, remote_id)
);

CREATE TABLE events (
	id             TEXT PRIMARY KEY,
	calendar_id    TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
	account_id     TEXT NOT NULL,
	remote_id      TEXT NOT NULL,
	series_remote  TEXT NOT NULL DEFAULT '',
	ical_uid       TEXT NOT NULL DEFAULT '',
	title          TEXT NOT NULL DEFAULT '',
	start_ms       INTEGER NOT NULL,
	end_ms         INTEGER NOT NULL,
	all_day        INTEGER NOT NULL DEFAULT 0,
	time_zone      TEXT NOT NULL DEFAULT '',
	location       TEXT NOT NULL DEFAULT '',
	description    TEXT NOT NULL DEFAULT '',
	attendees      TEXT NOT NULL DEFAULT '', -- JSON []Attendee
	organizer      TEXT NOT NULL DEFAULT '', -- JSON Address
	is_organizer   INTEGER NOT NULL DEFAULT 0,
	my_response    TEXT NOT NULL DEFAULT '',
	meeting_url    TEXT NOT NULL DEFAULT '',
	is_recurring   INTEGER NOT NULL DEFAULT 0,
	recurrence     TEXT NOT NULL DEFAULT '',
	rrule          TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT '',
	reminders      TEXT NOT NULL DEFAULT '', -- JSON []int
	can_edit       INTEGER NOT NULL DEFAULT 0,
	updated_ms     INTEGER NOT NULL DEFAULT 0,
	UNIQUE(calendar_id, remote_id)
);
CREATE INDEX events_calendar_start ON events(calendar_id, start_ms);
CREATE INDEX events_ical_uid ON events(ical_uid) WHERE ical_uid != '';

ALTER TABLE messages ADD COLUMN invite TEXT NOT NULL DEFAULT ''; -- JSON Invite of a text/calendar part
`,
	// 6: event etags (conditional updates)
	`
ALTER TABLE events ADD COLUMN etag TEXT NOT NULL DEFAULT '';
`,
	// 7: video call support per calendar
	`
ALTER TABLE calendars ADD COLUMN video_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN calendar_muted INTEGER NOT NULL DEFAULT 0; -- no event reminders
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
