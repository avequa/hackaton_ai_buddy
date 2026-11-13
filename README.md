# HackChange 2026 — AI IT-Buddy

Помощник стажёра на первые недели работы (кейс Авито). Стажёр пишет, что ему нужно, а помощник готовит заявки на доступы, оборудование и в техподдержку и подсказывает, что делать дальше по плану адаптации. Заявки отправляет сам стажёр, права проверяет сервер.

Данные синтетические. Инструкции в `ai-it-buddy/data/kb` тестовые и не являются регламентами Авито.

## Что в репозитории

```
ai-it-buddy/        прототип
  core/             ядро на Go: API для интерфейса, заявки, права, MCP-серверы
  agent/            агент на Python + LangGraph
  data/             сотрудники, сервисы, склад, план адаптации, база знаний
  deploy/           конфиги LLM-шлюза, Prometheus, Grafana
  docs/             API для фронтенда, TypeScript-контракты
  scripts/          проверка LLM и демо-сценарии
  compose.yaml      запуск всего проекта
llm-gateway/        LLM-шлюз на Go (собирается из compose)
```

Как всё связано:

```
интерфейс -> core -> agent -> llm-gateway -> LLM (OpenRouter)
               ^         |
               +-- MCP --+   агент вызывает инструменты core от имени пользователя
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

Первая сборка занимает 2–4 минуты. Проверка:

```bash
docker compose ps                  # все сервисы running или healthy
curl 127.0.0.1:8080/healthz        # ok
python3 scripts/check_llm.py       # модели отвечают и вызывают инструменты
python3 scripts/demo.py access     # один сценарий целиком
```

`check_llm.py` тратит 3 запроса к LLM, `demo.py` со всеми сценариями около 10. Бесплатный лимит OpenRouter — 50 запросов в день на аккаунт. Если лимит кончился, поставь в `.env` ключ другого аккаунта и перезапусти: `docker compose up -d --force-recreate llm-gateway`.

## Адреса

| Что | Адрес |
|---|---|
| API | http://127.0.0.1:8080/api |
| Схема API | http://127.0.0.1:8080/api/openapi.yaml |
| Grafana | http://127.0.0.1:3001 (дашборд AgentOps на главной) |
| Prometheus | http://127.0.0.1:9091 |
| LLM-шлюз | http://127.0.0.1:4001 |

Порты открыты только на локальной машине. Агент наружу не открыт: к нему обращается только core.

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

## Повседневные команды

Все команды запускаются из папки `ai-it-buddy`.

```bash
docker compose logs -f core agent         # логи
docker compose up -d --build core         # пересобрать один сервис после правок
docker compose restart agent              # перезапуск без пересборки
docker compose down                       # остановить
docker compose down -v                    # остановить и удалить данные Prometheus и Grafana
```

Заявки хранятся в памяти core, после перезапуска core они пропадают.

Включить и выключить инцидент для демо:

```bash
curl -X POST 127.0.0.1:8080/api/demo/incidents -H 'X-Demo-Token: demo' \
  -d '{"service":"vpn","title":"Недоступен шлюз VPN","active":true}'
curl -X POST 127.0.0.1:8080/api/demo/incidents -H 'X-Demo-Token: demo' \
  -d '{"service":"vpn","active":false}'
```

## Тесты и линтер

Тесты не обращаются к LLM и не тратят лимит.

core (Go):

```bash
cd ai-it-buddy/core
go test -race -cover ./...
go tool golangci-lint run ./...
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

Покрытие сейчас: core 93%, agent 99%, llm-gateway 93%.

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

## Правила

- Ключи храним только в `.env`. Он есть в `.gitignore`, в коммиты не попадает.
- Под каждую задачу заводим ветку и вливаем её в `main` через PR.
- Данные в `data/` только синтетические.
