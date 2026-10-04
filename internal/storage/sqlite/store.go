package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("protect storage: %w", err)
	}
	return s, nil
}

func (s *Store) initialize(ctx context.Context) error {
	for _, pragma := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA synchronous=FULL", "PRAGMA max_page_count=32768"} {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure SQLite: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)"); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var exists int
		err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version=?", entry.Name()).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES(?)", entry.Name())
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	// A crash after send cannot prove whether Telegram accepted the message.
	_, err = s.db.ExecContext(ctx, "UPDATE notifications SET status='uncertain',last_error='process stopped during delivery' WHERE status='sending'")
	return err
}

func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) SourceState(ctx context.Context, name string) (monitor.SourceState, error) {
	var st monitor.SourceState
	var initialized int
	var stamp int64
	err := s.db.QueryRowContext(ctx, "SELECT initialized,last_success,failures,last_error FROM source_states WHERE name=?", name).Scan(&initialized, &stamp, &st.Failures, &st.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	st.Initialized = initialized == 1
	if stamp > 0 {
		st.LastSuccess = time.Unix(stamp, 0).UTC()
	}
	return st, err
}
func (s *Store) Seen(ctx context.Context, item domain.Item) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM source_items WHERE source=? AND external_id=? AND text_hash=?", item.Source.Name, item.ExternalID, domain.Hash(item.Text)).Scan(&count)
	return count > 0, err
}

func (s *Store) CommitSource(ctx context.Context, info domain.SourceInfo, items []domain.ClassifiedItem, now time.Time, options monitor.RecordOptions) ([]domain.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is harmless
	var initialized int
	err = tx.QueryRowContext(ctx, "SELECT initialized FROM source_states WHERE name=?", info.Name).Scan(&initialized)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var events []domain.Event
	for _, classified := range items {
		item, c := classified.Item, domain.Guard(classified.Item, classified.Classification)
		if item.PublishedAt.IsZero() {
			item.PublishedAt = now
		}
		encoded, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO source_items(source,external_id,text_hash,published_at,detected_at,text,classification) VALUES(?,?,?,?,?,?,?)", info.Name, item.ExternalID, domain.Hash(item.Text), item.PublishedAt.Unix(), now.Unix(), item.Text, string(encoded))
		if err != nil {
			return nil, err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if inserted == 0 || c.Type == domain.NotRelevant || now.Sub(item.PublishedAt) > options.MaxAge || item.PublishedAt.After(now.Add(5*time.Minute)) {
			continue
		}
		groups, err := recentGroups(ctx, tx, item.PublishedAt)
		if err != nil {
			return nil, err
		}
		group := monitor.Correlate(item, c, groups)
		if group != nil && c.Type.Rank() <= group.Rank {
			continue
		}
		groupID := rand.Text()
		if group != nil {
			groupID = group.ID
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO reset_groups(id,scope,rank,latest_at,source,external_id,source_url,text,text_hash) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET rank=excluded.rank,latest_at=max(latest_at,excluded.latest_at),source=excluded.source,external_id=excluded.external_id,source_url=excluded.source_url,text=excluded.text,text_hash=excluded.text_hash", groupID, c.Scope, c.Type.Rank(), item.PublishedAt.Unix(), info.Name, item.ExternalID, item.URL, item.Text, domain.Hash(item.Text))
		if err != nil {
			return nil, err
		}
		event := domain.Event{ID: domain.Hash(groupID + " " + string(c.Type)), GroupID: groupID, Type: c.Type, Title: string(c.Type), Summary: c.Reason, Source: info, SourceURL: item.URL, ExternalID: item.ExternalID, PublishedAt: item.PublishedAt, DetectedAt: now, Confidence: c.Confidence, RawTextHash: domain.Hash(item.Text), Evidence: c.Evidence}
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO events(id,group_id,type,payload,detected_at) VALUES(?,?,?,?,?)", event.ID, groupID, c.Type, string(payload), now.Unix())
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE source_items SET event_id=? WHERE source=? AND external_id=? AND text_hash=?", event.ID, info.Name, item.ExternalID, event.RawTextHash)
		if err != nil {
			return nil, err
		}
		if initialized == 1 && c.Notify(options.NotifySignals) {
			delivery, err := json.Marshal(monitor.Notification{Event: &event, ChatID: options.ChatID})
			if err != nil {
				return nil, err
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO notifications(event_id,payload,chat_id,status,next_attempt,updated_at) VALUES(?,?,?,'pending',?,?)", event.ID, string(delivery), options.ChatID, now.Unix(), now.Unix())
			if err != nil {
				return nil, err
			}
		}
		events = append(events, event)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO source_states(name,initialized,last_success) VALUES(?,1,?) ON CONFLICT(name) DO UPDATE SET initialized=1,last_success=excluded.last_success,failures=0,last_error=''", info.Name, now.Unix())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

func recentGroups(ctx context.Context, tx *sql.Tx, stamp time.Time) ([]monitor.Group, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,scope,rank,latest_at,source,external_id,source_url,text,text_hash FROM reset_groups WHERE latest_at BETWEEN ? AND ? ORDER BY latest_at DESC LIMIT 100", stamp.Add(-36*time.Hour).Unix(), stamp.Add(36*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var groups []monitor.Group
	for rows.Next() {
		var g monitor.Group
		var at int64
		if err := rows.Scan(&g.ID, &g.Scope, &g.Rank, &at, &g.Source, &g.ExternalID, &g.URL, &g.Text, &g.Hash); err != nil {
			return nil, err
		}
		g.LatestAt = time.Unix(at, 0).UTC()
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *Store) SourceFailure(ctx context.Context, name, reason string, now time.Time, threshold int, adminChat string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	_, err = tx.ExecContext(ctx, "INSERT INTO source_states(name,failures,last_error) VALUES(?,1,?) ON CONFLICT(name) DO UPDATE SET failures=failures+1,last_error=excluded.last_error", name, reason)
	if err != nil {
		return err
	}
	var failures int
	var lastAlert int64
	if err := tx.QueryRowContext(ctx, "SELECT failures,last_alert FROM source_states WHERE name=?", name).Scan(&failures, &lastAlert); err != nil {
		return err
	}
	if adminChat != "" && failures >= threshold && now.Unix()-lastAlert > 86400 {
		payload, err := json.Marshal(monitor.Notification{Message: fmt.Sprintf("Watcher: источник %s недоступен %d проверок подряд. Подробности в метриках и логах.", name, failures), ChatID: adminChat})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO notifications(payload,chat_id,status,next_attempt,updated_at) VALUES(?,?,'pending',?,?)", string(payload), adminChat, now.Unix(), now.Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE source_states SET last_alert=? WHERE name=?", now.Unix(), name); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) SaveRun(ctx context.Context, r monitor.Run) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO check_runs(id,started_at,duration_ms,source_failures,events_created) VALUES(?,?,?,?,?)", r.ID, r.Started.Unix(), r.Duration.Milliseconds(), r.Failures, r.Events)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM check_runs WHERE started_at<?", r.Started.Add(-7*24*time.Hour).Unix())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM source_items WHERE detected_at<? AND event_id IS NULL", r.Started.Add(-30*24*time.Hour).Unix())
	return err
}
func (s *Store) Pending(ctx context.Context, now time.Time) (*monitor.Notification, error) {
	var n monitor.Notification
	var payload string
	err := s.db.QueryRowContext(ctx, "SELECT id,payload,attempts FROM notifications WHERE status='pending' AND next_attempt<=? ORDER BY id LIMIT 1", now.Unix()).Scan(&n.ID, &payload, &n.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id, attempts := n.ID, n.Attempts
	if err := json.Unmarshal([]byte(payload), &n); err != nil {
		return nil, err
	}
	n.ID = id
	n.Attempts = attempts
	return &n, nil
}
func (s *Store) Claim(ctx context.Context, id int64, now time.Time) (bool, error) {
	r, err := s.db.ExecContext(ctx, "UPDATE notifications SET status='sending',attempts=attempts+1,updated_at=? WHERE id=? AND status='pending'", now.Unix(), id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
func (s *Store) Finish(ctx context.Context, id int64, state string, messageID int64, reason string, next time.Time) error {
	r, err := s.db.ExecContext(ctx, "UPDATE notifications SET status=?,message_id=?,last_error=?,next_attempt=?,updated_at=? WHERE id=? AND status='sending'", state, messageID, reason, next.Unix(), time.Now().UTC().Unix(), id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("notification state transition rejected")
	}
	return nil
}
func (s *Store) Classification(ctx context.Context, hash string) (domain.Classification, bool, error) {
	var c domain.Classification
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM classifications WHERE hash=?", hash).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err == nil, err
}
func (s *Store) SaveClassification(ctx context.Context, hash string, c domain.Classification) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT OR REPLACE INTO classifications(hash,payload) VALUES(?,?)", hash, string(body))
	return err
}
