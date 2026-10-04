package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"golang.org/x/net/html"
)

type Documents struct {
	Client *httpio.Client
	URLs   []string
	Name   string
}

func (d Documents) Info() domain.SourceInfo {
	name := d.Name
	if name == "" {
		name = "openai-help"
	}
	return domain.SourceInfo{Name: name, Kind: domain.Official}
}

func PlainText(node *html.Node) string {
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "nav" || n.Data == "noscript") {
			return
		}
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
			text.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return strings.Join(strings.Fields(text.String()), " ")
}

func ParseDocument(body []byte) ([]string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse document: %w", err)
	}
	var article *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if article != nil {
			return
		}
		if n.Type == html.ElementNode && (n.Data == "article" || n.Data == "main") {
			article = n
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(root)
	if article == nil {
		return nil, errors.New("documentation article missing; source layout may have changed")
	}
	var paragraphs []string
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "nav" || n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "li") {
			if text := PlainText(n); len(text) > 20 {
				paragraphs = append(paragraphs, text)
			}
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(article)
	if len(paragraphs) == 0 {
		return nil, errors.New("documentation paragraphs missing")
	}
	return paragraphs, nil
}

func (d Documents) Fetch(ctx context.Context, _ time.Time) ([]domain.Item, error) {
	var items []domain.Item
	for _, address := range d.URLs {
		body, err := d.Client.Get(ctx, address, nil)
		if err != nil {
			return nil, fmt.Errorf("fetch documentation: %w", err)
		}
		var paragraphs []string
		if strings.HasSuffix(address, ".md") {
			for _, block := range strings.Split(string(body), "\n\n") {
				if len(strings.TrimSpace(block)) > 20 {
					paragraphs = append(paragraphs, strings.TrimSpace(block))
				}
			}
		} else {
			paragraphs, err = ParseDocument(body)
		}
		if err != nil {
			return nil, err
		}
		if len(paragraphs) == 0 {
			return nil, errors.New("documentation is empty")
		}
		for _, p := range paragraphs {
			items = append(items, domain.Item{Source: d.Info(), ExternalID: domain.Hash(address + " " + p), URL: address, Text: p})
		}
	}
	return items, nil
}
