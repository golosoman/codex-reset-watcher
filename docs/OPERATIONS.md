# Эксплуатация и восстановление

## Проверка без сообщений пользователю

```bash
cd /opt/apps/codex-reset-watcher
docker compose ps
curl -fsS http://127.0.0.1:8501/healthz
curl -fsS http://127.0.0.1:8501/readyz
curl -fsS http://127.0.0.1:8501/metrics
docker compose logs --tail=100 watcher
docker compose exec watcher /watcher validate-telegram
```

`validate-telegram` использует `getMe`, не отправляет сообщение. Успех не доказывает право писать выбранному получателю: проверь, что он ранее отправил боту `/start` и не заблокировал его. Первый baseline не является тестом реальной доставки. Доставку тестируем через `httptest`, а настоящий канал проверяем только согласованным сообщением или реальным новым событием.

`/readyz` возвращает 200, если локальное хранилище доступно: отказ одного внешнего источника не должен перезапускать весь сервис. В JSON отдельно видны `Initialized`, `LastSuccess`, `Failures`, `LastError`. Отсутствующий X token и HTTP 403 Help - degraded источники, не «новостей нет».

Для доступа с компьютера используй SSH tunnel, не публикуй порт:

```bash
ssh -L 8501:127.0.0.1:8501 robovps
```

## Обновление без сборки Go на VPS

Собери тестированный Linux amd64 бинарник локально (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o .deploy/watcher ./cmd/watcher`). Передай исходники и бинарник в каталог приложения, но не перезаписывай `.env` и `data`.

```bash
docker build -f deploy/Dockerfile -t codex-reset-watcher:<commit-sha> .deploy
WATCHER_IMAGE=codex-reset-watcher:<commit-sha> docker compose up -d --no-build
```

Сохрани прежний image tag перед обновлением. Для rollback передай его в `WATCHER_IMAGE` и повтори `up -d --no-build`. Старую БД не удаляй. Миграции только additive; если изменение схемы несовместимо, восстановление должно использовать согласованный backup, а не downgrade наугад.

## Backup SQLite

Не копируй только `watcher.db` у работающего WAL: часть данных может находиться в `-wal`. Для этого маленького сервиса самый простой безопасный вариант - короткая остановка и архив **всего data**:

```bash
docker compose stop watcher
install -d -m 700 backups
tar -czf backups/data-$(date -u +%Y%m%dT%H%M%SZ).tar.gz data
chmod 600 backups/*.tar.gz
docker compose start watcher
```

Переноси backup на другой компьютер/хранилище. Копия на том же VPS не защищает от потери VPS. Секреты сохраняются отдельно с ограниченными правами. После восстановления `data` должен принадлежать UID/GID 65532.

## Неопределённый результат Telegram

Статусы `uncertain` и `failed` сами не повторяются. Для разбора при необходимости установи операторский SQLite CLI на машине с backup или используй уже установленный на VPS. В БД лежит содержимое сообщений: не публикуй выгрузку.

```sql
SELECT id,status,attempts,last_error,message_id,datetime(updated_at,'unixepoch')
FROM notifications WHERE status IN ('failed','uncertain') ORDER BY id;
```

Для `uncertain` сначала проверь чат по времени/доказательству из `payload`. Если сообщение пришло, отметь `sent` с настоящим message_id. Если точно не пришло и повтор согласован, останови worker и явно верни конкретную запись в очередь:

```sql
UPDATE notifications SET status='pending',next_attempt=unixepoch(),last_error='operator-approved retry'
WHERE id=<checked-id> AND status IN ('failed','uncertain');
```

Не делай массовое обновление статусов и не обнуляй baseline для «проверки»: это создаёт повторы или скрывает новые события. Сохраняй backup перед ручными изменениями.

## Когда источник не отвечает

- 401/403 X: проверь доступ token к timeline и условия текущего тарифа X. Не вставляй token в issue.
- 429: адаптер учитывает Retry-After и ограничивает ожидание общим source timeout; цикл не продвигает watermark.
- 403 Help: не подменяй cookies и не отключай TLS. Выбери доступную официальную страницу через `OPENAI_HELP_URLS`; Status и docs остаются независимыми.
- Изменился HTML/JSON: `last_error` показывает ошибку адаптера; нужен fixture новой структуры и тест, а не молчаливое успешное чтение нуля элементов.
- Сбой Telegram: временный отказ повторяется с backoff, постоянный отказ требует исправить token/chat или разблокировать бота.

Если настроен `ADMIN_CHAT_ID`, после нескольких неудач приходит одно служебное предупреждение на источник в сутки. Для X без credentials таких предупреждений нет.

## Ресурсы и метрики

Контейнер ограничен 128 MiB RAM, SQLite основной файл примерно 128 MiB; также учитывай WAL, образы и backup. Логи ограничены 15 MiB на контейнер. Не удаляй БД при недостатке места: сначала перенеси старые backup и только проверенные неиспользуемые образы.

Основные признаки проблемы: источник `watcher_source_available=0`, возраст `watcher_last_success_timestamp` больше двух интервалов, рост `watcher_source_errors_total` или `watcher_notifications_total{status="uncertain"}`. Метрики доступны и без OTLP collector. Grafana этой задачей не запускается.

## Добавление источника

Новый API реализует `monitor.Source`: стабильное `Info()` и полное `Fetch(ctx,since)`. Ошибка любой страницы должна возвращать ошибку всего чтения. Сохраняй корректные UTC timestamps, внешний ID, исходный текст и HTTPS permalink. Ограничивай размер ответа; никогда не логируй URL с секретами. Добавь успешный fixture, сломанный JSON/HTML, timeout, даты и повторы в тестах; подключи адаптер в `cmd/watcher`. Новое имя получает независимый baseline.
