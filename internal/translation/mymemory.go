package translation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Cache interface {
	Translation(context.Context, string) (string, bool, error)
	SaveTranslation(context.Context, string, string) error
}

var errTranslationTimeout = errors.New("translation request timed out")
var errTranslationQuota = errors.New("translation quota exhausted")

// MyMemory sends only the public excerpt, never Telegram credentials or account data.
type MyMemory struct {
	HTTP    *http.Client
	Cache   Cache
	BaseURL string
	mu      sync.Mutex
	retryAt time.Time
}

func (m *MyMemory) Translate(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("empty translation input")
	}
	var latin, cyrillic int
	for _, r := range text {
		if unicode.In(r, unicode.Latin) {
			latin++
		}
		if unicode.In(r, unicode.Cyrillic) {
			cyrillic++
		}
	}
	if cyrillic > latin {
		return text, nil
	}
	if len(text) > 2800 {
		return "", errors.New("translation input too large")
	}
	sum := sha256.Sum256([]byte("mymemory-en-ru-v1\x00" + text))
	key := hex.EncodeToString(sum[:])
	if m.Cache != nil {
		saved, ok, err := m.Cache.Translation(ctx, key)
		if err != nil {
			return "", errors.New("translation cache unavailable")
		}
		if ok {
			return saved, nil
		}
	}
	m.mu.Lock()
	blocked := time.Now().Before(m.retryAt)
	m.mu.Unlock()
	if blocked {
		return "", errors.New("translation temporarily unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Expand the domain term so machine translation does not turn "banked" into a bank transaction.
	text = strings.NewReplacer("banked resets", "saved manual quota resets", "banked reset", "saved manual quota reset", "Banked reset", "Saved manual quota reset").Replace(text)
	var result []string
	for _, chunk := range chunks(text) {
		translated, err := m.segment(ctx, chunk)
		if err != nil {
			// A failed translator must not consume the delivery worker's whole timeout.
			m.mu.Lock()
			pause := time.Minute
			if errors.Is(err, errTranslationQuota) {
				pause = 15 * time.Minute
			}
			m.retryAt = time.Now().Add(pause)
			m.mu.Unlock()
			return "", err
		}
		result = append(result, translated)
	}
	translated := strings.Join(result, " ")
	if m.Cache != nil {
		// A cache write failure must not discard an already available translation.
		_ = m.Cache.SaveTranslation(ctx, key, translated)
	}
	return translated, nil
}

func (m *MyMemory) segment(ctx context.Context, text string) (string, error) {
	base := m.BaseURL
	if base == "" {
		base = "https://api.mymemory.translated.net/get"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+url.Values{"q": {text}, "langpair": {"en|ru"}, "mt": {"1"}}.Encode(), nil)
	if err != nil {
		return "", errors.New("create translation request")
	}
	res, err := m.HTTP.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errTranslationTimeout
		}
		return "", errors.New("translation transport unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusTooManyRequests {
			return "", errTranslationQuota
		}
		return "", errors.New("translation request rejected")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 64*1024+1))
	if err != nil || len(body) > 64*1024 {
		return "", errors.New("invalid translation response size")
	}
	var data struct {
		Status int  `json:"responseStatus"`
		Quota  bool `json:"quotaFinished"`
		Data   struct {
			Text string `json:"translatedText"`
		} `json:"responseData"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", errors.New("invalid translation JSON")
	}
	if data.Quota || data.Status == 429 {
		return "", errTranslationQuota
	}
	if data.Status != 200 {
		return "", errors.New("translation unavailable")
	}
	translated := strings.TrimSpace(html.UnescapeString(data.Data.Text))
	if translated == "" || !utf8.ValidString(translated) || len(translated) > 8192 {
		return "", errors.New("invalid translated text")
	}
	hasRussian := false
	for _, r := range translated {
		hasRussian = hasRussian || unicode.In(r, unicode.Cyrillic)
	}
	if !hasRussian {
		return "", errors.New("translation is not Russian")
	}
	return translated, nil
}

// MyMemory accepts at most 500 UTF-8 bytes; prefer word boundaries without losing characters.
func chunks(text string) []string {
	var result []string
	for len(text) > 480 {
		end := 480
		for !utf8.RuneStart(text[end]) {
			end--
		}
		if boundary := strings.LastIndexAny(text[:end], " \n\t"); boundary > end/2 {
			end = boundary + 1
		}
		result = append(result, text[:end])
		text = text[end:]
	}
	if text != "" {
		result = append(result, text)
	}
	return result
}
