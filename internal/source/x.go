package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
)

type X struct {
	Client                   *httpio.Client
	Token, Username, BaseURL string
	userID                   string
}

func (*X) Info() domain.SourceInfo {
	return domain.SourceInfo{Name: "tibo-x", Kind: domain.FirstParty, ResetContext: true}
}
func (x *X) Fetch(ctx context.Context, since time.Time) ([]domain.Item, error) {
	if x.Token == "" {
		return nil, ErrUnavailable
	}
	base := x.BaseURL
	if base == "" {
		base = "https://api.x.com/2"
	}
	headers := http.Header{"Authorization": []string{"Bearer " + x.Token}}
	if x.userID == "" {
		body, err := x.Client.Get(ctx, base+"/users/by/username/"+url.PathEscape(x.Username), headers)
		if err != nil {
			return nil, fmt.Errorf("lookup X user: %w", err)
		}
		var data struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &data); err != nil || data.Data.ID == "" {
			return nil, errors.New("invalid X user response")
		}
		x.userID = data.Data.ID
	}
	var items []domain.Item
	pagination := ""
	for page := 0; page < 20; page++ {
		query := url.Values{"max_results": {"100"}, "tweet.fields": {"created_at"}, "exclude": {"retweets"}, "start_time": {since.UTC().Format(time.RFC3339)}}
		if pagination != "" {
			query.Set("pagination_token", pagination)
		}
		body, err := x.Client.Get(ctx, base+"/users/"+url.PathEscape(x.userID)+"/tweets?"+query.Encode(), headers)
		if err != nil {
			return nil, fmt.Errorf("fetch X posts: %w", err)
		}
		var data struct {
			Data []struct {
				ID, Text string
				Created  time.Time `json:"created_at"`
			} `json:"data"`
			Meta *struct {
				Next string `json:"next_token"`
			} `json:"meta"`
			Errors []json.RawMessage `json:"errors"`
		}
		if err := json.Unmarshal(body, &data); err != nil || data.Meta == nil || len(data.Errors) > 0 {
			return nil, errors.New("invalid or partial X timeline response")
		}
		for _, post := range data.Data {
			if post.ID == "" || post.Created.IsZero() {
				return nil, errors.New("X post missing ID or date")
			}
			items = append(items, domain.Item{Source: x.Info(), ExternalID: post.ID, URL: "https://x.com/" + x.Username + "/status/" + post.ID, Text: post.Text, PublishedAt: post.Created})
		}
		pagination = data.Meta.Next
		if pagination == "" {
			return items, nil
		}
	}
	return nil, errors.New("X pagination limit exceeded; cursor not advanced")
}
