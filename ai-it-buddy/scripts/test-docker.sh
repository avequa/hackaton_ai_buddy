#!/usr/bin/env bash
# Все тесты проекта в Docker: на машине нужен только Docker.
# Запуск из любой папки: bash ai-it-buddy/scripts/test-docker.sh
# Модель не вызывается. Исходники монтируются только на чтение, в рабочей папке ничего не создается.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
id=buddy-test-$$
net=$id
pg=$id-pg

cleanup() {
  docker rm -f "$pg" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create "$net" >/dev/null
echo "Поднимаем временный Postgres для тестов..."
docker run -d --name "$pg" --network "$net" \
  -e POSTGRES_PASSWORD=test -e POSTGRES_DB=buddy_test postgres:17-alpine >/dev/null
for _ in $(seq 60); do
  docker exec "$pg" pg_isready -U postgres -d buddy_test >/dev/null 2>&1 && break
  sleep 1
done

rows=()

# Go: кэш модулей и сборки в именованных томах, второй запуск быстрый
go_test() {
  local module=$1
  echo "== $module (первый раз качаются зависимости Go)"
  local total
  total=$(docker run --rm --network "$net" \
    -v "$root":/src:ro \
    -v buddy-test-gomod:/go/pkg/mod -v buddy-test-gocache:/root/.cache/go-build \
    -e TEST_DATABASE_URL="postgres://postgres:test@$pg:5432/buddy_test?sslmode=disable" \
    -e GOFLAGS=-buildvcs=false \
    -w "/src/$module" golang:1.26 \
    sh -c 'go test -p 1 -count=1 -coverprofile=/tmp/cover.out ./... >/tmp/test.log 2>&1 || { cat /tmp/test.log; exit 1; }
           go tool cover -func=/tmp/cover.out | awk "/^total:/ {print \$3}"')
  rows+=("$module|$total")
}

go_test ai-it-buddy/core
go_test ai-it-buddy/loggen
go_test llm-gateway
go_test incident-radar

echo "== ai-it-buddy/agent (ставим зависимости Python, первый раз 1-3 минуты)"
total=$(docker run --rm \
  -v "$root/ai-it-buddy/agent":/src:ro -v buddy-test-pip:/root/.cache/pip \
  -e COVERAGE_FILE=/tmp/.coverage -e PYTHONDONTWRITEBYTECODE=1 \
  -w /src python:3.13-slim \
  sh -c 'pip install -q --disable-pip-version-check -r requirements-dev.txt >/tmp/pip.log 2>&1 || { cat /tmp/pip.log; exit 1; }
         python -m pytest -q -p no:cacheprovider --cov=buddy_agent --cov-report=term >/tmp/test.log 2>&1 || { cat /tmp/test.log; exit 1; }
         awk "/^TOTAL/ {print \$NF}" /tmp/test.log')
rows+=("ai-it-buddy/agent|$total")

# web: копия без node_modules (на хосте они могут быть собраны под другую ОС)
echo "== web (ставим зависимости Node, первый раз 1-2 минуты)"
total=$(docker run --rm \
  -v "$root/web":/src:ro -v buddy-test-npm:/root/.npm \
  node:22-slim \
  sh -c 'mkdir /w && cd /src && tar --exclude=./node_modules --exclude=./dist --exclude=./coverage -cf - . | tar -C /w -xf - && cd /w
         npm ci --no-audit --no-fund >/tmp/npm.log 2>&1 || { cat /tmp/npm.log; exit 1; }
         npx vitest run --coverage --coverage.reporter=text-summary --coverage.reporter=json-summary >/tmp/test.log 2>&1 || { cat /tmp/test.log; exit 1; }
         node -e "console.log(require(\"/w/coverage/coverage-summary.json\").total.lines.pct + \"%\")"')
rows+=("web|$total")

echo "== алерты Prometheus"
docker run --rm -v "$root/ai-it-buddy/deploy/prometheus":/p:ro -w /p --entrypoint promtool \
  prom/prometheus:v3.15.1 test rules alerts_test.yml

printf '\nмодуль                 покрытие строк\n'
for r in "${rows[@]}"; do printf '%-22s %s\n' "${r%%|*}" "${r#*|}"; done
