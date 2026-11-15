# HackChange 2026 — AvitoBuddy

Прототип для хакатона Hack&Change 2026, кейс Авито «AI-buddy: ИИ-наставник для стажёров». Помощник стажёра на первые недели работы (кейс Авито). Стажёр пишет, что ему нужно, а помощник готовит заявки на доступы, оборудование и в техподдержку и подсказывает, что делать дальше по плану адаптации. Заявки отправляет сам стажёр, права проверяет сервер.

Данные синтетические. Инструкции в `ai-it-buddy/data/kb` тестовые и не являются регламентами Авито.

## Что в репозитории

```
ai-it-buddy/        прототип
  core/             ядро на Go: API для интерфейса, заявки, права, MCP-серверы, Postgres
  agent/            агент на Python + LangGraph
  loggen/           генератор логов NGINX для демо сбоев
  data/             сотрудники, сервисы, склад, план адаптации, база знаний
  deploy/           конфиги LLM-шлюза, Prometheus (с алертами), Grafana
  docs/             API для фронтенда, TypeScript-контракты
  openspec/         спецификации поведения (OpenSpec) и архив изменений
  scripts/          проверка LLM, демо-сценарии, покрытие
  compose.yaml      запуск всего проекта
llm-gateway/        LLM-шлюз на Go
incident-radar/     разбор access-логов NGINX, MCP-сервер
web/                интерфейс: React + Vite, в Docker - nginx (alpine)
.github/workflows/  CI: линтеры, тесты, покрытие, спецификации, алерты, сборка образов
```

Как всё связано:

```
интерфейс -> core -> agent -> llm-gateway -> LLM (OpenRouter)
              | ^        |
              | +-- MCP -+   агент вызывает инструменты core от имени пользователя
              |
              +-> Postgres         заявки, уведомления, база знаний
              +-> incident-radar   раз в 10 секунд: доля 5xx в логах VPN и GitLab -> инцидент сам
```

Подробнее про устройство: `ai-it-buddy/README.md`. API для фронтенда: `ai-it-buddy/docs/api.md`.

## Что нужно

- Docker с Compose v2 (`docker compose version`).
- Python 3.10+ для скриптов проверки (ставить ничего не надо, только стандартная библиотека).
- Ключ OpenRouter, бесплатный и без карты: https://openrouter.ai/settings/keys

Для разработки без Docker нужны Go 1.26 и Python 3.13.

## Варианты запуска

| Вариант | Для чего | Что нужно |
|---|---|---|
| 1. Всё в Docker | демо, проверка, запись видео | Docker |
| 2. Без ключа LLM | интерфейс, заявки, сбои, наставник - всё, кроме чата | Docker |
| 3. Бэкенд в Docker + фронт локально | разработка интерфейса с горячей перезагрузкой | Docker, Node 22 |
| 4. Только бэкенд, сценарии из терминала | проверка без браузера | Docker, Python 3.10+ |
| 5. Без Docker | разработка ядра и агента | Go 1.26, Python 3.13, Postgres 16+ |

### 1. Всё в Docker

```bash
cd ai-it-buddy
cp .env.example .env
```

Открой `.env` и впиши ключ целиком, без кавычек:

```
OPENROUTER_API_KEY=sk-or-v1-...
```

Сборка и запуск:

```bash
docker compose up -d --build
```

Первая сборка занимает 3–5 минут. Проверка:

```bash
docker compose ps                  # все сервисы running или healthy
curl 127.0.0.1:3000/readyz         # {"status":"ok","storage":"postgres",...} - фронт и ядро связаны
python3 scripts/check_llm.py       # модели отвечают и вызывают инструменты
```

Интерфейс: **http://127.0.0.1:3000**. Вход без пароля: выбрать сотрудника. Сценарий показа по шагам - `ai-it-buddy/docs/AvitoBuddy_demo_scenario.pdf`.

### 2. Без ключа LLM

То же, что вариант 1, но `.env` можно не заполнять. Работает всё, кроме чата: стартовый набор, заявки, согласования, путь адаптации, сбои (ручные и по логам), уведомления, сводка наставнику. Чат ответит «Помощник сейчас недоступен», а `llm-gateway` в `docker compose ps` будет перезапускаться с ошибкой про пустой ключ - это ожидаемо.

### 3. Бэкенд в Docker, фронт локально

```bash
cd ai-it-buddy && docker compose up -d --build     # бэкенд
cd ../web && npm install && npm run dev            # http://localhost:5173
```

Vite проксирует `/api` на ядро `127.0.0.1:8080`, правки в `web/src` видны сразу.

### 4. Только бэкенд, сценарии из терминала

```bash
cd ai-it-buddy && docker compose up -d --build
python3 scripts/demo.py                    # все сценарии кроме restart, ~10 запросов к LLM
python3 scripts/demo.py starter radar      # без LLM
python3 scripts/demo.py restart            # перезапуск ядра: данные на месте
```

Сценарии: `access`, `notbyrole`, `incident`, `equipment`, `onboarding`, `sre`, `starter`, `security`, `radar`, `restart`.

### 5. Без Docker

Ядро без `DATABASE_URL` работает только в памяти, без `RADAR_URL` - без автоматических сбоев, без `AGENT_URL` - без чата. Минимум:

```bash
cd ai-it-buddy/core
JWT_SECRET=dev-secret-dev-secret-dev-secret DATA_DIR=../data DEMO_TOKEN=demo go run ./cmd/core   # :8080
cd ../../web && npm install && npm run dev                                                         # :5173
```

Полный набор переменных ядра - в `ai-it-buddy/README.md` и `ai-it-buddy/compose.yaml`.

### Ключ и лимит модели

`check_llm.py` тратит 3 запроса к LLM, `demo.py` со всеми сценариями около 10. Сценарии `starter`, `radar` и `restart` модель не вызывают. Бесплатный лимит OpenRouter — 50 запросов в день на аккаунт. Если лимит кончился, поставь в `.env` ключ другого аккаунта и перезапусти: `docker compose up -d --force-recreate llm-gateway`.

## Адреса

| Что | Адрес |
|---|---|
| **Интерфейс** | http://127.0.0.1:3000 |
| API | http://127.0.0.1:8080/api |
| Схема API | http://127.0.0.1:8080/api/openapi.yaml |
| Grafana | http://127.0.0.1:3001 (дашборд AgentOps на главной) |
| Prometheus | http://127.0.0.1:9091 |
| Алерты | http://127.0.0.1:9091/alerts |
| LLM-шлюз | http://127.0.0.1:4001 |
| Сбои для демо (loggen) | http://127.0.0.1:8092/outages |

Порты открыты только на локальной машине. Агент, Postgres и incident-radar наружу не открыты.

Интерфейс в Docker отдает nginx: он же проксирует `/api` на ядро, поэтому браузеру нужен один адрес и CORS не нужен.

## Тестовые пользователи

Пароля нет, это имитация корпоративного входа:

```bash
curl -X POST 127.0.0.1:8080/api/auth/login -d '{"user_id":"anna.intern"}'
```

| user_id | Кто |
|---|---|
| `anna.intern` | backend-стажёр, команда платежей |
| `pavel.intern` | frontend-стажёр, команда личного кабинета |
| `igor.mentor` | наставник Анны |
| `maria.mentor` | наставник Павла |
| `olga.lead` | руководитель обеих команд |
| `sergey.sre` | дежурный SRE, объявляет и закрывает сбои |

Все сотрудники вымышленные, список - `ai-it-buddy/data/seed/users.yaml`.

## Повседневные команды

Все команды запускаются из папки `ai-it-buddy`.

```bash
docker compose logs -f core agent         # логи
docker compose up -d --build core         # пересобрать один сервис после правок
docker compose restart agent              # перезапуск без пересборки
docker compose down                       # остановить
docker compose down -v                    # остановить и удалить все данные: заявки, метрики, логи
docker compose exec postgres psql -U buddy  # посмотреть базу руками
```

Заявки и уведомления хранятся в Postgres и переживают перезапуск. Сбросить демо к начальному состоянию: `docker compose down -v`.

Сбой по логам для демо: VPN начинает отвечать 5xx, через 20–30 секунд инцидент открывается сам, после выключения закрывается сам.

```bash
curl -X POST 127.0.0.1:8092/outages -d '{"service":"vpn","error_rate":0.7}'   # включить
curl -X POST 127.0.0.1:8092/outages -d '{"service":"vpn","error_rate":0}'     # выключить
python3 scripts/demo.py radar                                                  # то же сценарием, с выводом
```

## Тесты и линтер

Тесты не обращаются к LLM и не тратят лимит. Все модули и итоговое покрытие одной командой (нужны Go 1.26, Python 3.13; Postgres для интеграционных тестов поднимется в Docker сам):

```bash
ai-it-buddy/scripts/coverage.sh
```

То же самое запускает CI на каждый push и PR, плюс проверку спецификаций OpenSpec, тесты алертов Prometheus, линтер OpenAPI и сборку всех образов.

По модулям, core (Go):

```bash
cd ai-it-buddy/core
TEST_DATABASE_URL=postgres://... go test -race -p 1 -cover ./...   # без переменной тесты на Postgres пропускаются
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...
```

agent (Python):

```bash
cd ai-it-buddy/agent
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements-dev.txt
pytest --cov=buddy_agent
```

llm-gateway (Go):

```bash
cd llm-gateway
go test -race -cover ./...
go tool golangci-lint run ./...
```

Покрытие сейчас: core 92%, agent 99%, llm-gateway 90%, incident-radar 93%, loggen 73%.

Спецификации и алерты:

```bash
cd ai-it-buddy && npx @fission-ai/openspec@2 validate --all --strict
cd ai-it-buddy/deploy/prometheus && promtool test rules alerts_test.yml
```

## Другой провайдер LLM

Модели задаются в `ai-it-buddy/deploy/llm-gateway.yaml`. Агент обращается не к конкретной модели, а к роли (`main`, `small`, `validator`), поэтому при смене провайдера код не меняется.

Подходит любой OpenAI-совместимый API: поменяй блоки `upstreams` и `targets` в конфиге и впиши ключ в `.env`. Для DeepSeek конфиг уже есть, в `.env` нужно добавить две строки:

```
LLM_CONFIG=llm-gateway.deepseek.yaml
DEEPSEEK_API_KEY=...
```

## Если что-то не работает

| Симптом | Что делать |
|---|---|
| `port is already allocated` | Порт занят другим проектом. Найди его через `docker ps` и останови, или поменяй порт слева в `compose.yaml` |
| core не стартует, в логах ошибка про `data` | Запусти compose из папки `ai-it-buddy`, а не из корня |
| `check_llm.py`: HTTP 402 или 429 | Кончился лимит или баланс. Поставь ключ другого аккаунта |
| `check_llm.py`: 404 `unavailable for free` | OpenRouter убрал бесплатную модель. Подбери другую на https://openrouter.ai/models?max_price=0&supported_parameters=tools и замени ID в `deploy/llm-gateway.yaml` |
| Чат отвечает 502 | Смотри `docker compose logs agent llm-gateway` |
| Интерфейс не открывается на :3000 | `docker compose ps web` и `docker compose logs web`; в `compose.yaml` должен быть сервис `web`, а папка `web` - рядом с `ai-it-buddy` |
| `npm run dev`: ошибка про `@rolldown/binding-win32` | `node_modules` поставлены из Windows, а запуск в WSL/Linux: `rm -rf node_modules && npm install` |
| Шлюз не стартует: `... is empty` | В `.env` нет ключа или он пустой |
| core пишет `postgres is not reachable` и выходит | Postgres не поднялся за 30 секунд: `docker compose logs postgres` |
| `/readyz` показывает `degraded` | База недоступна, ядро работает из памяти и допишет изменения позже. Не перезапускай core, пока база не вернется |
| Инцидент по логам не открывается | `docker compose logs core incident-radar loggen`; в Prometheus `buddy_radar_checks_total{result="error"}` |

## Правила

- Ключи храним только в `.env`. Он есть в `.gitignore`, в коммиты не попадает.
- Под каждую задачу заводим ветку и вливаем её в `main` через PR.
- Данные в `data/` только синтетические.
- Изменение поведения начинается со спецификации: `ai-it-buddy/openspec/changes/<имя>/` (proposal, design, tasks, дельта спецификации), после реализации - `openspec archive <имя>`. Примеры в `openspec/changes/archive/`.
