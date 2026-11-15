# Покрытие тестами

Покрытие строк прикладного кода, написанного командой. Зависимости, сгенерированный код и сами тесты не считаются. Замер от 16.11.2026.

| Модуль | Язык | Покрытие строк | Инструмент |
|---|---|---|---|
| ai-it-buddy/core | Go | 91.6% | `go test -coverprofile` |
| ai-it-buddy/agent | Python | 99% | `pytest-cov` |
| web | TypeScript | 94.7% | `vitest --coverage` (v8) |
| llm-gateway | Go | 89.8% | `go test -coverprofile` |
| incident-radar | Go | 93.4% | `go test -coverprofile` |
| ai-it-buddy/loggen | Go | 89.2% | `go test -coverprofile` |

## Как повторить

Нужен только Docker:

```bash
bash ai-it-buddy/scripts/test-docker.sh      # из корня репозитория, печатает таблицу выше
```

Скрипт поднимает временный Postgres, прогоняет тесты каждого модуля в контейнере с нужным языком, проверяет алерты Prometheus и печатает итог. Исходники монтируются только на чтение.

Если Go 1.26, Python 3.13 и Node.js 22 стоят локально: `bash ai-it-buddy/scripts/coverage.sh`. Тот же замер на каждый push делает CI (`.github/workflows/ci.yml`), итоги видны в Summary запуска.

По модулям:

| Модуль | Команда | Подробный отчёт |
|---|---|---|
| core | `cd ai-it-buddy/core && go test -race -p 1 -coverprofile=coverage.out ./... && go tool cover -func=coverage.out` | `go tool cover -html=coverage.out` |
| agent | `cd ai-it-buddy/agent && python -m pytest --cov=buddy_agent --cov-report=term-missing` | `--cov-report=html` |
| web | `cd web && npm run coverage` | `web/coverage/index.html` |
| llm-gateway, incident-radar, loggen | `go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out` | `go tool cover -html=coverage.out` |

## Что проверяют тесты

- **core:**
  - права и политика ролей;
  - жизненный цикл заявок;
  - склад, план адаптации, уведомления, тревоги, сводка наставника;
  - автоинциденты с гистерезисом;
  - запись в Postgres и восстановление после перезапуска, работа при недоступной базе;
  - полнотекстовый поиск;
  - HTTP API и MCP-инструменты.
- **agent:** граф с поддельной моделью, вызовы инструментов, проверка обещаний черновиков, ошибки модели и ядра.
- **web:** приложение целиком на поддельном бэкенде, все экраны и роли.
- **Алерты Prometheus:** юнит-тесты `promtool` (`deploy/prometheus/alerts_test.yml`).
