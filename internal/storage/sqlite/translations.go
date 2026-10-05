package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) Translation(ctx context.Context, hash string) (string, bool, error) {
	var text string
	err := s.db.QueryRowContext(ctx, "SELECT translated_text FROM translations WHERE hash=?", hash).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return text, err == nil, err
}

func (s *Store) SaveTranslation(ctx context.Context, hash, text string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO translations(hash,translated_text,created_at) VALUES(?,?,?) ON CONFLICT(hash) DO NOTHING", hash, text, time.Now().UTC().Unix())
	return err
}
