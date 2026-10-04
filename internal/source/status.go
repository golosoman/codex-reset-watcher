package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
)

type Status struct {
	Client *httpio.Client
	URL    string
}

func (Status) Info() domain.SourceInfo {
	return domain.SourceInfo{Name: "openai-status", Kind: domain.Official}
}
func (s Status) Fetch(ctx context.Context, since time.Time) ([]domain.Item, error) {
	address := s.URL
	if address == "" {
		address = "https://status.openai.com/api/v2/incidents.json"
	}
	body, err := s.Client.Get(ctx, address, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch status: %w", err)
	}
	var data struct {
		Incidents []struct {
			Name    string `json:"name"`
			URL     string `json:"shortlink"`
			Updates []struct {
				ID      string    `json:"id"`
				Body    string    `json:"body"`
				Created time.Time `json:"created_at"`
			} `json:"incident_updates"`
		} `json:"incidents"`
	}
	if err := json.Unmarshal(body, &data); err != nil || data.Incidents == nil {
		return nil, errors.New("invalid status response")
	}
	var items []domain.Item
	for _, incident := range data.Incidents {
		for _, update := range incident.Updates {
			if update.ID == "" || update.Created.IsZero() {
				return nil, errors.New("status update missing ID or date")
			}
			if update.Created.After(since) {
				items = append(items, domain.Item{Source: s.Info(), ExternalID: update.ID, URL: incident.URL, Text: incident.Name + "\n" + update.Body, PublishedAt: update.Created})
			}
		}
	}
	return items, nil
}
