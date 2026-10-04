package source

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"golang.org/x/net/html"
)

type Feed struct {
	Client            *httpio.Client
	Name, URL, Format string
	Derived           bool
}

func (f Feed) Info() domain.SourceInfo {
	return domain.SourceInfo{Name: f.Name, Kind: domain.Community, ResetContext: f.Derived || f.Name == "reddit-codex"}
}

func plain(raw string) string {
	root, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	return PlainText(root)
}

func withOrigin(item domain.Item, raw string) domain.Item {
	item.CanonicalAuthor, item.CanonicalOriginID, item.CanonicalOriginURL = CanonicalOrigin(item.URL)
	if item.CanonicalOriginID != "" {
		return item
	}
	root, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return item
	}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if item.CanonicalOriginID != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					item.CanonicalAuthor, item.CanonicalOriginID, item.CanonicalOriginURL = CanonicalOrigin(a.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	return item
}
func date(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unsupported feed date")
}

func ParseFeed(body []byte, format string, info domain.SourceInfo) ([]domain.Item, error) {
	var items []domain.Item
	if format == "json" {
		var data struct {
			Items []struct {
				ID         string `json:"id"`
				ExternalID string `json:"external_id"`
				URL        string `json:"url"`
				Title      string `json:"title"`
				Text       string `json:"text"`
				Content    string `json:"content_text"`
				HTML       string `json:"content_html"`
				Published  string `json:"date_published"`
				Timestamp  string `json:"published_at"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &data); err != nil || data.Items == nil {
			return nil, errors.New("invalid JSON feed")
		}
		for _, entry := range data.Items {
			id := entry.ID
			if id == "" {
				id = entry.ExternalID
			}
			text := entry.Text
			if text == "" {
				text = entry.Content
			}
			if text == "" {
				text = plain(entry.HTML)
			}
			stamp := entry.Published
			if stamp == "" {
				stamp = entry.Timestamp
			}
			published, err := date(stamp)
			if err != nil {
				return nil, err
			}
			items = append(items, withOrigin(domain.Item{Source: info, ExternalID: id, URL: entry.URL, Text: strings.TrimSpace(entry.Title + "\n" + text), PublishedAt: published}, entry.HTML))
		}
	} else {
		var data struct {
			XMLName xml.Name
			Channel struct {
				Items []struct {
					GUID        string `xml:"guid"`
					Title       string `xml:"title"`
					Link        string `xml:"link"`
					Description string `xml:"description"`
					Content     string `xml:"encoded"`
					Published   string `xml:"pubDate"`
				} `xml:"item"`
			} `xml:"channel"`
			Entries []struct {
				ID    string `xml:"id"`
				Title string `xml:"title"`
				Links []struct {
					Href string `xml:"href,attr"`
					Rel  string `xml:"rel,attr"`
				} `xml:"link"`
				Summary   string `xml:"summary"`
				Content   string `xml:"content"`
				Published string `xml:"published"`
				Updated   string `xml:"updated"`
			} `xml:"entry"`
		}
		if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&data); err != nil {
			return nil, fmt.Errorf("parse RSS/Atom: %w", err)
		}
		if data.XMLName.Local != "rss" && data.XMLName.Local != "feed" {
			return nil, errors.New("expected RSS or Atom root")
		}
		for _, entry := range data.Channel.Items {
			id := entry.GUID
			if id == "" {
				id = entry.Link
			}
			published, err := date(entry.Published)
			if err != nil {
				return nil, err
			}
			text := entry.Content
			if text == "" {
				text = entry.Description
			}
			items = append(items, withOrigin(domain.Item{Source: info, ExternalID: id, URL: entry.Link, Text: entry.Title + "\n" + plain(text), PublishedAt: published}, text))
		}
		for _, entry := range data.Entries {
			link := ""
			for _, l := range entry.Links {
				if l.Rel == "alternate" || l.Rel == "" {
					link = l.Href
					break
				}
			}
			stamp := entry.Published
			if stamp == "" {
				stamp = entry.Updated
			}
			published, err := date(stamp)
			if err != nil {
				return nil, err
			}
			text := entry.Content
			if text == "" {
				text = entry.Summary
			}
			items = append(items, withOrigin(domain.Item{Source: info, ExternalID: entry.ID, URL: link, Text: entry.Title + "\n" + plain(text), PublishedAt: published}, text))
		}
	}
	for _, item := range items {
		if item.ExternalID == "" || strings.TrimSpace(item.Text) == "" || item.PublishedAt.IsZero() {
			return nil, errors.New("feed item missing stable ID, text or publication date")
		}
	}
	if len(items) > 1000 {
		return nil, errors.New("feed contains too many items")
	}
	return items, nil
}

func (f Feed) Fetch(ctx context.Context, since time.Time) ([]domain.Item, error) {
	body, err := f.Client.Get(ctx, f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	items, err := ParseFeed(body, f.Format, f.Info())
	if err != nil {
		return nil, &monitor.SourceProblem{Health: "degraded", Reason: err.Error()}
	}
	result := items[:0]
	for _, item := range items {
		item.SourceFetchedAt = time.Now().UTC()
		author, _, _ := CanonicalOrigin(item.URL)
		if f.Derived && author == "thsottiaux" {
			item.Source.Kind = domain.FirstPartyDerived
		}
		item.Observations = Observations(item)
		if item.PublishedAt.IsZero() || item.PublishedAt.After(since) {
			result = append(result, item)
		}
	}
	return result, nil
}
