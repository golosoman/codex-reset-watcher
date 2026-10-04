# Бесплатный мониторинг: проверка контрактов

Дата проверки: 2026-10-05. На robovps все пять новых публичных URL ответили HTTP 200 обычному HTTP-клиенту, без cookies, прокси обхода и X credentials. Внешняя доступность не гарантируется навсегда.

## Фактические контракты

[Feed](https://codex-reset.com/api/feed) содержит `tweets`, `profile.handle`, `fetched_at`, `stale`; у поста есть `id`, `url`, `text`, `at`, `is_reply`, `in_reply_to_tweet_id`, `conversation_id`. Поля classifier агрегатора не используются как истина. Derived разрешён только при точном совпадении профиля `thsottiaux`, автора permalink и ID публикации. Исходная дата не заменяется временем проверки. Quote/reply context сохраняется, если передан, но не придумывается из ссылки.

[Timeline](https://codex-reset.com/api/timeline) содержит `events`, `updated_at`, а у события `summary`, `announced_at`, `reset_kind`, `source`, optional `official_window`. Это другой контракт и другая ответственность. Даже summary с оригинальным X URL остаётся `aggregator`. `source=observed` может дать сохранённое наблюдение распространения banked reset, но не first-party подтверждение. Дополнительные HTML-графики/statistics в JSON игнорируются.

[Twiscan](https://twiscan.com/en/x/thsottiaux) отдаёт две формы текста: длинные блоки `clamp-{id}-0` и **короткие посты без ID в `whitespace-pre-wrap`**. Короткое «Reset all propagated» нельзя пропускать. При разборе второго вида ID берётся только из предшествующего блока автора в том же контейнере. Чужие репосты исключаются; timestamp извлекается из X snowflake, поскольку timezone HTML не обозначен. Это trust стороннего транспорта, не криптографическое доказательство аутентичности публикации. Изменение разметки с нулём сообщений сразу degraded, а не healthy.

[Community RSS](https://community.openai.com/latest.rss) - RSS с датами и содержимым обсуждений. Staff не определяется по нику. [Reddit RSS](https://www.reddit.com/r/codex/new/.rss) - Atom; локальная сеть ответила 403, robovps ответил 200. Обход не используется. Если обе площадки недоступны, primary Tibo monitoring не останавливается.

## Доказательства и переходы

Origin URL/ID сохраняется и для RSS-перепечатки, если X permalink находится в HTML-ссылке. Это не повышает trust перепечатки. Один origin в feed, timeline и Reddit остаётся одним исходным сообщением; разные transport records сохраняются для разбора. Самостоятельное наблюдение «Plus 2% → 100%» и «Pro 5x ещё ждёт» сохраняется отдельно от цитаты анонса.

Тарифы: Plus / Pro / Pro 5x / Pro 20x / Business / unknown. Статусы: received / not_received / banked_reset_seen. Обычный периодический reset, смена модели и outage не являются global reset. Состояния signal, announced, imminent, confirmed, propagating, completed имеют возрастающий rank. Banked, global и unknown не выдаются друг за друга; неопределённый тип может уточняться по новым доказательствам.

Неоднозначная корреляция не угадывается только по времени: могут получиться отдельные группы. Это сознательная граница эвристики, а не обещание идеального понимания любых формулировок. Publication, aggregator fetch и watcher detection хранятся раздельно.

## Проверки

Fixtures содержат сокращённые реальные анонс и completion 2 октября, плюс синтетическое banked observation. Исторические тексты используются только офлайн: тесты не отправляют новости в реальный Telegram. Отдельный тест проходит announcement -> Plus received -> Pro waiting -> completed, сохраняет всю evidence и три уведомления вместо уведомления на каждую копию.

Live probe без секретов и рассылки:

```bash
WATCHER_LIVE_TEST=1 go test -run TestPublicSourcesLive -v ./internal/source
```

В локальной проверке разобраны 27 feed-публикаций, 66 timeline-событий, 30 Community topics и 19 постов/ответов Twiscan. Числа меняются с содержимым источников. Обычный CI полностью автономный; live probe opt-in.

Секреты пользователя ограничены существующим Telegram token/chat. LLM выключен. Основной путь не зависит от внешних платных API; optional RSSHub владелец подключает только к разрешённому feed.
