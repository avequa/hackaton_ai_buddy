# ai-it-buddy

Бэкенд AvitoBuddy: ядро, агент, имитации корпоративных систем, данные, мониторинг и docker compose для всего проекта. Общее описание и порядок проверки — в [README в корне](../README.md).

## Состав

| Папка | Что это |
|---|---|
| `core/` | ядро на Go: HTTP API для интерфейса, MCP-серверы для агента, права, заявки, уведомления, Postgres, автоинциденты |
| `agent/` | агент на Python (LangGraph): выбирает MCP-инструменты ядра и отвечает пользователю |
| `loggen/` | генератор access-логов NGINX для VPN и GitLab с включаемым сбоем |
| `data/seed/` | сотрудники, сервисы и проекты, роли, склад, план адаптации |
| `data/kb/` | база знаний: 7 тестовых инструкций |
| `deploy/` | конфиг LLM-шлюза, Prometheus и алерты, Grafana |
| `openspec/` | спецификации поведения (OpenSpec) и архив изменений |
| `docs/` | API для интерфейса, TypeScript-контракты, отчёт о покрытии, сценарий демо |
| `scripts/` | `check_llm.py`, `demo.py`, `test-docker.sh`, `coverage.sh` |
| `compose.yaml` | запуск всего проекта, включая `../web`, `../llm-gateway` и `../incident-radar` |

## Запуск

```bash
cp .env.example .env              # OPENROUTER_API_KEY
docker compose up -d --build
python3 scripts/check_llm.py      # модели отвечают и вызывают инструменты, 3 запроса к LLM
python3 scripts/demo.py           # сценарии из терминала, около 10 запросов к LLM
python3 scripts/demo.py starter radar restart   # без LLM
```

Сценарии `demo.py`: `access`, `notbyrole`, `incident`, `equipment`, `onboarding`, `sre`, `starter`, `security`, `radar`, `restart`.

Без Docker, только ядро в памяти (без базы, чата и автоинцидентов):

```bash
cd core
JWT_SECRET=dev-secret-dev-secret-dev-secret DATA_DIR=../data DEMO_TOKEN=demo go run ./cmd/core
```

## Ядро

**API для интерфейса.** `/api/*`, схема — `core/internal/httpapi/openapi.yaml`, описание по экранам — [docs/api.md](docs/api.md). Вход: `POST /api/auth/login {"user_id": ...}` → токен JWT на 12 часов.

**MCP для агента.** `/mcp/access`, `/mcp/helpdesk`, `/mcp/inventory`, `/mcp/onboarding`, 12 инструментов:

| Сервер | Инструменты |
|---|---|
| access | `list_services`, `list_my_access`, `list_access_requests`, `draft_access_request` |
| helpdesk | `check_service_health`, `search_kb`, `draft_ticket`, `list_tickets` |
| inventory | `list_equipment`, `list_equipment_requests`, `draft_equipment_request` |
| onboarding | `get_onboarding` |

**Права.**
- Пользователь в инструменте берётся из токена запроса. Аргументы инструментов идентификатор пользователя не принимают.
- Токен агента (audience `mcp`) живёт 5 минут и годится только для MCP. Подтвердить заявку им нельзя.
- Чужие заявки, шаги и стажёры выглядят как несуществующие (404). Попытка записывается как событие безопасности.
- Политика роли: уровень доступа, команда проекта. Нетипичная заявка помечается `needs_mentor` и после подтверждения уходит наставнику.
- Сообщение стажёра проверяется до агента на попытки обойти правила. Наставник получает уведомление, но не чаще раза в 10 минут по одному стажёру.

**Хранение.**
- Ядро отвечает из памяти и раз в секунду пишет изменения в Postgres одной транзакцией.
- Неудачная запись повторяется со следующей пачкой, а `/readyz` показывает `degraded`.
- При старте состояние восстанавливается из базы. Пустая база заполняется из `data/seed`.
- Миграции встроены в бинарник.
- Поиск по базе знаний — полнотекстовый по-русски, с запасным поиском в памяти.

**Автоинциденты.**
- Раз в 10 секунд ядро вызывает `analyze_logs` у incident-radar за последние 30 секунд лога каждого сервиса.
- Открытие: доля 5xx не меньше 20% две проверки подряд (минимум 20 запросов в окне).
- Закрытие: меньше 5% три проверки подряд.
- Радар закрывает только свои инциденты, объявленные людьми не трогает.

**Переменные окружения**

| Переменная | По умолчанию | Что |
|---|---|---|
| `JWT_SECRET` | — (обязательна) | ключ подписи токенов |
| `DATA_DIR` | `./data` | справочники и база знаний |
| `DATABASE_URL` | пусто — только память | Postgres |
| `AGENT_URL` | пусто — чат выключен | агент |
| `RADAR_URL`, `RADAR_TOKEN`, `RADAR_SERVICES` | пусто, —, `vpn,gitlab` | incident-radar |
| `RADAR_INTERVAL`, `RADAR_WINDOW` | `10s`, `30s` | частота и окно проверок |
| `APPROVAL_DELAY` | `20s` (в compose `15s`) | через сколько отвечают имитированные согласующие |
| `DIGEST_INTERVAL` | `168h` | рассылка сводок наставникам |
| `DEMO_TOKEN` | пусто — демо-ручки выключены | `X-Demo-Token` для `/api/demo/*` |
| `CORS_ALLOWED_ORIGINS` | — | origin интерфейса при прямом доступе к API |
| `CORE_ADDR`, `LOG_FORMAT` | `:8080`, `json` | адрес и формат логов |

## Агент

`agent/buddy_agent`:
- `graph.py` — граф LangGraph: модель → инструменты → модель → проверка ответа;
- `guard.py` — проверка, что помощник не пообещал черновик, которого не создал;
- `prompts.py` — системный промпт.

Контракт с ядром: `POST /chat {message, thread_id, token, user}` → `{reply, tools_used}`. Переменные: `LLM_BASE_URL`, `LLM_MODEL` (роль в шлюзе, `main`), `CORE_MCP_URL`, `LLM_TIMEOUT`, `AGENT_MAX_STEPS`, `AGENT_HISTORY_MESSAGES`.

## Мониторинг

Grafana, дашборд AgentOps:
- вопросы, закрытые без наставника;
- конверсия черновик → подтверждение;
- тикеты, не созданные во время сбоев;
- попытки выйти за права;
- доля 5xx по логам;
- запись в базу;
- токены, задержка, переключения на запасную модель.

Метрики ядра начинаются с `buddy_`, шлюза — с `llm_gateway_`. Алерты — в `deploy/prometheus/alerts.yml`, их тесты — в `alerts_test.yml` (`promtool test rules alerts_test.yml`).

## Тесты

```bash
bash scripts/test-docker.sh            # все модули проекта в Docker, ничего ставить не нужно
bash scripts/coverage.sh               # то же на локальных Go, Python и Node
cd core && go test -race -p 1 -cover ./...     # с TEST_DATABASE_URL=postgres://... — ещё тесты на Postgres
cd agent && pip install -r requirements-dev.txt && python -m pytest --cov=buddy_agent
cd loggen && go test -cover .
```

Отчёт: [docs/coverage.md](docs/coverage.md).
