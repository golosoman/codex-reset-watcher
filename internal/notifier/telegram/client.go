package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
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
	Translator     Translator
	Logger         *slog.Logger
}

type Translator interface {
	Translate(context.Context, string) (string, error)
}

func Format(n monitor.Notification) string {
	return format(n, "")
}

func format(n monitor.Notification, translated string) string {
	if n.Event == nil {
		return html.EscapeString(n.Message)
	}
	e := n.Event
	titles := map[domain.EventType]string{domain.GlobalResetConfirmed: "✅ Сброс лимитов подтверждён", domain.BankedResetConfirmed: "🎁 Дополнительный сброс лимитов доступен", domain.ResetAnnounced: "📅 Объявлен сброс лимитов", domain.ResetImminent: "⏳ Сброс лимитов ожидается скоро", domain.ResetSignal: "👀 Возможный сброс лимитов", domain.ResetCompleted: "✅ Выдача сброса лимитов завершена"}
	titles[domain.ResetPropagating] = "⏳ Пользователи начинают получать сброс лимитов"
	titles[domain.ResetConfirmed] = "✅ Сброс подтверждён; тип пока не уточнён"
	headline := titles[e.Type]
	details := domain.Limit(strings.NewReplacer("reset", "сброс лимитов", "Reset", "Сброс лимитов").Replace(e.Summary), 400)
	if e.Type == domain.ResetSignal {
		details = "Есть ранний сигнал. Сам сброс лимитов пока не подтверждён."
	}
	if e.Type == domain.BankedResetConfirmed {
		details = "Сообщается о дополнительном сбросе лимитов. Его может потребоваться применить вручную в настройках использования."
	}
	if e.Scope == "banked" {
		details += "\nЭто сохранённый ручной сброс, не автоматическое восстановление квоты. Специально тратить текущую квоту не требуется."
	}
	if e.Type == domain.ResetPropagating {
		details = "Есть наблюдения пользователей. Полное распространение пока не подтверждено."
		if e.Scope == "banked" {
			details += "\nРечь о сохранённом ручном сбросе, не об автоматическом восстановлении квоты."
		}
	}
	if e.Scope == "unknown" {
		details += "\nТип сброса пока не уточнён: это не обещание автоматического восстановления квоты."
	}
	if e.Source.Kind == domain.Aggregator || e.Source.Kind == domain.Community || e.Source.Kind == domain.Unverified {
		details += "\nЭто сообщение агрегатора/сообщества, не официальное подтверждение OpenAI."
	}
	for _, observation := range e.Observations[:min(len(e.Observations), 6)] {
		status := map[string]string{"received": "получен", "not_received": "пока не получен", "banked_reset_seen": "появился ручной reset"}[observation.Status]
		if status == "" {
			status = "нет данных"
		}
		details += "\n" + domain.Limit(observation.Plan, 40) + ": " + status
	}
	if e.ExpectedWindow != "" {
		details += "\nОжидаемое время (как указано источником): " + domain.Limit(e.ExpectedWindow, 160)
	}
	text := "<b>" + headline + "</b>\nCodex / ChatGPT\n\n" + html.EscapeString(domain.Limit(details, 1000))
	if translated != "" {
		text += "\n\n<b>Перевод на русский</b>\n" + html.EscapeString(domain.Limit(translated, 1100)) + "\n<i>Машинный перевод; возможны неточности.</i>"
	} else {
		text += "\n\n<i>Перевод сейчас недоступен. Русское пояснение приведено выше.</i>"
	}
	text += "\n\n<b>Оригинал · " + html.EscapeString(domain.Limit(e.Source.Name, 80)) + "</b>\n<blockquote>" + html.EscapeString(domain.Limit(e.Evidence, 700)) + "</blockquote>"
	if u, err := url.Parse(e.SourceURL); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
		text += "\n<a href=\"" + html.EscapeString(e.SourceURL) + "\">Открыть источник</a>"
	}
	if u, err := url.Parse(e.CanonicalOriginURL); err == nil && u.Scheme == "https" && u.Host == "x.com" {
		text += "\n<a href=\"" + html.EscapeString(e.CanonicalOriginURL) + "\">Оригинальная публикация</a>"
	}
	if e.Source.Kind == domain.FirstPartyDerived {
		text += "\nПубликация Tibo получена через сторонний публичный источник."
	}
	text += "\n\nОбнаружено: " + e.DetectedAt.UTC().Format("02.01.2006 15:04 UTC")
	text += "\nПубликация не проверяет фактические лимиты твоего аккаунта."
	if n.Message != "" {
		text = html.EscapeString(domain.Limit(n.Message, 200)) + "\n\n" + text
	}
	return text
}

func (c Client) Send(ctx context.Context, n monitor.Notification) (int64, error) {
	var translated string
	if n.Event != nil && c.Translator != nil {
		var err error
		translated, err = c.Translator.Translate(ctx, domain.Limit(n.Event.Evidence, 700))
		if err != nil && c.Logger != nil {
			c.Logger.WarnContext(ctx, "translation unavailable; sending original and Russian explanation", "error", err)
		}
	}
	payload := map[string]any{"chat_id": n.ChatID, "text": format(n, translated), "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}}
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
