package sqlite

import (
	"context"
	"encoding/json"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

func (s *Store) History(ctx context.Context) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT payload FROM events ORDER BY detected_at DESC,rowid DESC LIMIT 10")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	history := []domain.Event{}
	for rows.Next() {
		var raw string
		var event domain.Event
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, err
		}
		history = append(history, event)
	}
	return history, rows.Err()
}

type EventEvidence struct {
	Item           domain.Item           `json:"item"`
	Classification domain.Classification `json:"classification"`
}
type DeliveryDebug struct {
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	MessageID int64  `json:"message_id"`
	Error     string `json:"error"`
}
type EventDebug struct {
	Event         domain.Event    `json:"event"`
	Evidence      []EventEvidence `json:"evidence"`
	Notifications []DeliveryDebug `json:"notifications"`
}

func (s *Store) EventDebug(ctx context.Context, id string) (EventDebug, error) {
	var result EventDebug
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT payload FROM events WHERE id=?", id).Scan(&raw); err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(raw), &result.Event); err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT payload,classification FROM source_items WHERE group_id=? ORDER BY published_at,source LIMIT 100", result.Event.GroupID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item, classification string
		var evidence EventEvidence
		if err := rows.Scan(&item, &classification); err != nil {
			_ = rows.Close()
			return result, err
		}
		if err := json.Unmarshal([]byte(item), &evidence.Item); err != nil {
			_ = rows.Close()
			return result, err
		}
		if err := json.Unmarshal([]byte(classification), &evidence.Classification); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.Evidence = append(result.Evidence, evidence)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = s.db.QueryContext(ctx, "SELECT status,attempts,coalesce(message_id,0),last_error FROM notifications WHERE event_id IN (SELECT id FROM events WHERE group_id=?) ORDER BY id", result.Event.GroupID)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var delivery DeliveryDebug
		if err := rows.Scan(&delivery.Status, &delivery.Attempts, &delivery.MessageID, &delivery.Error); err != nil {
			return result, err
		}
		result.Notifications = append(result.Notifications, delivery)
	}
	return result, rows.Err()
}
