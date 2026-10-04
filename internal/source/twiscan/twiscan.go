package twiscan

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"github.com/golosoman/codex-reset-watcher/internal/source"
	"golang.org/x/net/html"
)

type Source struct {
	Client *httpio.Client
	URL    string
}

func (Source) Info() domain.SourceInfo {
	return domain.SourceInfo{Name: "twiscan-tibo", Kind: domain.Unverified, ResetContext: true}
}
func (Source) FallbackFor() string { return "codex-reset-feed" }

func (s Source) Fetch(ctx context.Context, since time.Time) ([]domain.Item, error) {
	body, err := s.Client.Get(ctx, s.URL, nil)
	if err != nil {
		return nil, err
	}
	items, err := Parse(body)
	if err != nil {
		return nil, err
	}
	result := items[:0]
	for _, item := range items {
		if item.PublishedAt.After(since) {
			result = append(result, item)
		}
	}
	return result, nil
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func permalinkID(link string) string {
	for _, prefix := range []string{"https://twiscan.com/en/x/thsottiaux/", "https://x.com/thsottiaux/status/"} {
		if strings.HasPrefix(link, prefix) {
			id := strings.TrimPrefix(link, prefix)
			if _, err := strconv.ParseUint(id, 10, 64); err == nil {
				return id
			}
		}
	}
	return ""
}

func precedingPermalink(n *html.Node) string {
	if n.Parent == nil {
		return ""
	}
	var find func(*html.Node) string
	find = func(node *html.Node) string {
		if node.Type == html.ElementNode && node.Data == "a" {
			if id := permalinkID(attr(node, "href")); id != "" {
				return id
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if id := find(c); id != "" {
				return id
			}
		}
		return ""
	}
	for sibling := n.Parent.FirstChild; sibling != nil && sibling != n; sibling = sibling.NextSibling {
		if id := find(sibling); id != "" {
			return id
		}
	}
	return ""
}

// Parse uses full text blocks and exact account permalinks, excluding quoted/reposted authors.
func Parse(body []byte) ([]domain.Item, error) {
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	texts := map[string]string{}
	links := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "script" || n.Data == "style" {
				return
			}
			id := attr(n, "id")
			if strings.HasPrefix(id, "clamp-") && strings.HasSuffix(id, "-0") {
				texts[strings.TrimSuffix(strings.TrimPrefix(id, "clamp-"), "-0")] = source.PlainText(n)
			}
			// Short posts/replies have no clamp ID; the preceding account block owns their permalink.
			if n.Data == "div" && strings.Contains(attr(n, "class"), "whitespace-pre-wrap") {
				if postID := precedingPermalink(n); postID != "" {
					texts[postID] = source.PlainText(n)
				}
			}
			if n.Data == "a" {
				if id := permalinkID(attr(n, "href")); id != "" {
					links[id] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	var items []domain.Item
	for id, text := range texts {
		if !links[id] || text == "" {
			continue
		}
		// X snowflakes encode the publication timestamp; the viewer's display timezone is unspecified.
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil || n < 1<<22 {
			continue
		}
		stamp := time.UnixMilli(int64(n>>22) + 1288834974657).UTC()
		link := "https://x.com/thsottiaux/status/" + id
		info := (Source{}).Info()
		info.Kind = domain.FirstPartyDerived
		items = append(items, domain.Item{Source: info, ExternalID: id, URL: "https://twiscan.com/en/x/thsottiaux/" + id, CanonicalOriginURL: link, CanonicalOriginID: id, CanonicalAuthor: "thsottiaux", Text: text, PublishedAt: stamp, SourceFetchedAt: time.Now().UTC(), IsReply: strings.HasPrefix(text, "@")})
	}
	if len(items) == 0 {
		return nil, &monitor.SourceProblem{Health: "degraded", Reason: "Twiscan has no parseable posts: probable parser breakage"}
	}
	if len(items) > 1000 {
		return nil, &monitor.SourceProblem{Health: "degraded", Reason: "too many Twiscan posts"}
	}
	return items, nil
}
