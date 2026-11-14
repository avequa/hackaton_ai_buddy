# HackChange 2026 — AI IT-Buddy

Помощник стажёра на первые недели работы (кейс Авито). Стажёр пишет, что ему нужно, а помощник готовит заявки на доступы, оборудование и в техподдержку и подсказывает, что делать дальше по плану адаптации. Заявки отправляет сам стажёр, права проверяет сервер.

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

## Запуск

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
curl 127.0.0.1:8080/readyz         # {"status":"ok","storage":"postgres",...}
python3 scripts/check_llm.py       # модели отвечают и вызывают инструменты
python3 scripts/demo.py access     # один сценарий целиком
```

`check_llm.py` тратит 3 запроса к LLM, `demo.py` со всеми сценариями около 10. Сценарии `starter`, `radar` и `restart` модель не вызывают. Бесплатный лимит OpenRouter — 50 запросов в день на аккаунт. Если лимит кончился, поставь в `.env` ключ другого аккаунта и перезапусти: `docker compose up -d --force-recreate llm-gateway`.

## Адреса

| Что | Адрес |
|---|---|
| API | http://127.0.0.1:8080/api |
| Схема API | http://127.0.0.1:8080/api/openapi.yaml |
| Grafana | http://127.0.0.1:3001 (дашборд AgentOps на главной) |
| Prometheus | http://127.0.0.1:9091 |
| Алерты | http://127.0.0.1:9091/alerts |
| LLM-шлюз | http://127.0.0.1:4001 |
| Сбои для демо (loggen) | http://127.0.0.1:8092/outages |

Порты открыты только на локальной машине. Агент, Postgres и incident-radar наружу не открыты.

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
| Шлюз не стартует: `... is empty` | В `.env` нет ключа или он пустой |
| core пишет `postgres is not reachable` и выходит | Postgres не поднялся за 30 секунд: `docker compose logs postgres` |
| `/readyz` показывает `degraded` | База недоступна, ядро работает из памяти и допишет изменения позже. Не перезапускай core, пока база не вернется |
| Инцидент по логам не открывается | `docker compose logs core incident-radar loggen`; в Prometheus `buddy_radar_checks_total{result="error"}` |

## Правила

- Ключи храним только в `.env`. Он есть в `.gitignore`, в коммиты не попадает.
- Под каждую задачу заводим ветку и вливаем её в `main` через PR.
- Данные в `data/` только синтетические.
- Изменение поведения начинается со спецификации: `ai-it-buddy/openspec/changes/<имя>/` (proposal, design, tasks, дельта спецификации), после реализации - `openspec archive <имя>`. Примеры в `openspec/changes/archive/`.
