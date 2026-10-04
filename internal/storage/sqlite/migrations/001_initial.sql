CREATE TABLE source_states (
 name TEXT PRIMARY KEY, initialized INTEGER NOT NULL DEFAULT 0,
 last_success INTEGER NOT NULL DEFAULT 0, failures INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '', last_alert INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE source_items (
 source TEXT NOT NULL, external_id TEXT NOT NULL, text_hash TEXT NOT NULL,
 published_at INTEGER NOT NULL, detected_at INTEGER NOT NULL, text TEXT NOT NULL,
 classification TEXT NOT NULL, event_id TEXT,
 PRIMARY KEY (source,external_id,text_hash)
);
CREATE TABLE reset_groups (
 id TEXT PRIMARY KEY, scope TEXT NOT NULL, rank INTEGER NOT NULL,
 latest_at INTEGER NOT NULL, source TEXT NOT NULL, external_id TEXT NOT NULL,
 source_url TEXT NOT NULL, text TEXT NOT NULL, text_hash TEXT NOT NULL
);
CREATE TABLE events (
 id TEXT PRIMARY KEY, group_id TEXT NOT NULL REFERENCES reset_groups(id),
 type TEXT NOT NULL, payload TEXT NOT NULL, detected_at INTEGER NOT NULL,
 UNIQUE(group_id,type)
);
CREATE TABLE notifications (
 id INTEGER PRIMARY KEY, event_id TEXT UNIQUE REFERENCES events(id),
 payload TEXT NOT NULL, chat_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('pending','sending','sent','uncertain','failed')),
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL,
 updated_at INTEGER NOT NULL, message_id INTEGER, last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX notifications_pending ON notifications(status,next_attempt);
CREATE INDEX reset_groups_recent ON reset_groups(latest_at);
CREATE TABLE classifications (hash TEXT PRIMARY KEY,payload TEXT NOT NULL);
CREATE TABLE check_runs (
 id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, duration_ms INTEGER NOT NULL,
 source_failures INTEGER NOT NULL, events_created INTEGER NOT NULL
);
