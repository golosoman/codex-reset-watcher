package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
)

type Client struct {
	HTTP           *http.Client
	Token, BaseURL string
}

func Format(n monitor.Notification) string {
	if n.Event == nil {
		return html.EscapeString(n.Message)
	}
	e := n.Event
	titles := map[domain.EventType]string{domain.GlobalResetConfirmed: "✅ Сброс лимитов подтверждён", domain.BankedResetConfirmed: "🎁 Reset в запасе подтверждён", domain.ResetAnnounced: "📅 Объявлен reset лимитов", domain.ResetImminent: "⏳ Reset ожидается скоро", domain.ResetSignal: "👀 Возможный reset лимитов", domain.ResetCompleted: "✅ Распространение reset завершено"}
	headline := titles[e.Type]
	details := e.Summary
	if e.Type == domain.ResetSignal {
		details = "Есть ранний сигнал. Сам reset пока не подтверждён."
	}
	if e.Type == domain.BankedResetConfirmed {
		details = "Сообщается о reset в запасе. Его может потребоваться применить вручную в настройках использования."
	}
	text := "<b>" + headline + "</b>\nCodex / ChatGPT\n\n" + html.EscapeString(details) + "\n\n<b>" + html.EscapeString(e.Source.Name) + "</b>\n<blockquote>" + html.EscapeString(domain.Limit(e.Evidence, 700)) + "</blockquote>"
	if u, err := url.Parse(e.SourceURL); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
		text += "\n<a href=\"" + html.EscapeString(e.SourceURL) + "\">Открыть источник</a>"
	}
	text += "\n\nОбнаружено: " + e.DetectedAt.UTC().Format("02.01.2006 15:04 UTC")
	text += "\nПубликация не проверяет фактические лимиты твоего аккаунта."
	return text
}

func (c Client) Send(ctx context.Context, n monitor.Notification) (int64, error) {
	payload := map[string]any{"chat_id": n.ChatID, "text": Format(n), "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	base := strings.TrimSuffix(c.BaseURL, "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+c.Token+"/sendMessage", bytes.NewReader(raw))
	if err != nil {
		return 0, errors.New("create notification request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return 0, errors.New("notification connection unavailable")
		}
		return 0, monitor.AmbiguousDelivery{}
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64*1024+1))
	if err != nil || len(body) > 64*1024 {
		return 0, monitor.AmbiguousDelivery{}
	}
	var result struct {
		OK        bool `json:"ok"`
		ErrorCode int  `json:"error_code"`
		Result    struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		if res.StatusCode >= 400 {
			return 0, &monitor.DeliveryRejected{Code: res.StatusCode, RetryAfter: httpio.RetryAfter(res.Header.Get("Retry-After"))}
		}
		return 0, monitor.AmbiguousDelivery{}
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 && result.OK && result.Result.MessageID > 0 {
		return result.Result.MessageID, nil
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 && result.ErrorCode == 0 {
		return 0, monitor.AmbiguousDelivery{}
	}
	code := res.StatusCode
	if result.ErrorCode != 0 {
		code = result.ErrorCode
	}
	return 0, &monitor.DeliveryRejected{Code: code, RetryAfter: time.Duration(max(result.Parameters.RetryAfter, 0)) * time.Second}
}

func (c Client) Validate(ctx context.Context) error {
	base := strings.TrimSuffix(c.BaseURL, "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/bot"+c.Token+"/getMe", nil)
	if err != nil {
		return errors.New("create Telegram validation request")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("telegram validation transport failure")
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16*1024))
	if err != nil {
		return errors.New("read Telegram validation response")
	}
	var data struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &data); err != nil || !data.OK || res.StatusCode != 200 {
		return errors.New("Telegram credentials rejected: " + strconv.Itoa(res.StatusCode))
	}
	return nil
}
