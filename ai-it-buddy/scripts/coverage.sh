#!/usr/bin/env bash
# Тесты и покрытие всех модулей репозитория одной командой. Запускать из корня HackChange или из ai-it-buddy.
# Для интеграционных тестов с Postgres поднимается временный контейнер (если есть Docker),
# иначе они пропускаются. LLM не вызывается.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

pg=""
if [[ -z "${TEST_DATABASE_URL:-}" ]] && command -v docker >/dev/null 2>&1; then
  pg=$(docker run -d --rm -e POSTGRES_PASSWORD=test -e POSTGRES_DB=buddy_test -p 127.0.0.1:55432:5432 postgres:17-alpine)
  trap 'docker stop "$pg" >/dev/null' EXIT
  for _ in $(seq 30); do docker exec "$pg" pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
  export TEST_DATABASE_URL="postgres://postgres:test@127.0.0.1:55432/buddy_test?sslmode=disable"
fi
[[ -z "${TEST_DATABASE_URL:-}" ]] && echo "Docker нет и TEST_DATABASE_URL не задан: тесты на Postgres будут пропущены" >&2

rows=()
for m in ai-it-buddy/core ai-it-buddy/loggen llm-gateway incident-radar; do
  echo "== $m"
  (cd "$m" && go test -race -p 1 -count=1 -coverprofile=coverage.out ./... >/dev/null)
  rows+=("$m|$(cd "$m" && go tool cover -func=coverage.out | awk '/^total:/ {print $3}')")
done

echo "== ai-it-buddy/agent"
(
  cd ai-it-buddy/agent
  [[ -d .venv ]] || { python3 -m venv .venv && .venv/bin/pip install -q -r requirements-dev.txt; }
  .venv/bin/python -m pytest -q --cov=buddy_agent --cov-report=term >/tmp/agent-cov.txt
)
rows+=("ai-it-buddy/agent|$(awk '/^TOTAL/ {print $NF}' /tmp/agent-cov.txt)")

echo "== web"
(
  cd web
  [[ -d node_modules ]] || npm ci --no-audit --no-fund >/dev/null
  npx vitest run --coverage --coverage.reporter=json-summary >/dev/null
)
rows+=("web|$(node -e "console.log(require('./web/coverage/coverage-summary.json').total.lines.pct + '%')")")

printf '\nмодуль                 покрытие\n'
for r in "${rows[@]}"; do printf '%-22s %s\n' "${r%%|*}" "${r#*|}"; done
