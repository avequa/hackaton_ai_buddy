#!/usr/bin/env python3
"""Проверка LLM через llm-gateway: каждая роль отвечает и умеет вызывать инструменты.

Запуск (шлюз уже поднят на 127.0.0.1:4001):
    python3 scripts/check_llm.py
    python3 scripts/check_llm.py --url http://127.0.0.1:4001 --runs 3

Что проверяется (3 запроса, бесплатный лимит OpenRouter - 50 запросов в день):
  - small и main: вызов инструмента - модель должна вернуть tool_calls с валидным JSON в arguments
    и правильно заполнить поля по описанию задачи стажёра (заодно проверяет, что роль вообще отвечает);
  - validator: обычный ответ на русском (судье инструменты не нужны).
Код выхода 0 - все проверки прошли, 1 - хотя бы одна упала.
Только стандартная библиотека Python, ничего ставить не нужно.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request

TOOL_ROLES = ("small", "main")
TEXT_ROLES = ("validator",)

# Инструмент в том виде, в каком его будет видеть агент (упрощенная копия Access MCP)
SUGGEST_ACCESS_TOOL = {
    "type": "function",
    "function": {
        "name": "draft_access_request",
        "description": "Создать черновик заявки на доступ к корпоративному сервису. "
        "Заявка не отправляется, пока пользователь не подтвердит ее.",
        "parameters": {
            "type": "object",
            "properties": {
                "service": {"type": "string", "enum": ["gitlab", "grafana", "jira", "confluence"]},
                "project": {"type": "string", "description": "проект или репозиторий"},
                "level": {"type": "string", "enum": ["read", "write", "admin"]},
                "justification": {"type": "string", "description": "зачем нужен доступ, одной фразой"},
            },
            "required": ["service", "project", "level", "justification"],
            "additionalProperties": False,
        },
    },
}

TOOL_PROMPT = (
    "Я стажёр в команде платежей. Мне дали задачу поправить алерт в дашборде payments-api, "
    "его нужно отредактировать. Оформи черновик заявки на нужный доступ."
)


def post(url: str, body: dict, timeout: float) -> tuple[int, dict, float]:
    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"})
    start = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, json.load(resp), time.monotonic() - start
    except urllib.error.HTTPError as err:
        try:
            payload = json.load(err)
        except ValueError:
            payload = {"error": {"message": err.reason}}
        return err.code, payload, time.monotonic() - start


def check_text(url: str, role: str, timeout: float) -> tuple[bool, str]:
    status, payload, took = post(url, {
        "model": role,
        "messages": [{"role": "user", "content": "Ответь одним коротким предложением: что такое VPN?"}],
    }, timeout)
    if status != 200:
        return False, f"HTTP {status}: {payload.get('error', {}).get('message', payload)}"
    text = (payload["choices"][0]["message"].get("content") or "").strip()
    if not text:
        return False, "пустой ответ"
    return True, f"{took:.1f}s, {payload.get('usage', {}).get('total_tokens', '?')} токенов: {text[:80]}"


def check_tool(url: str, role: str, timeout: float) -> tuple[bool, str]:
    status, payload, took = post(url, {
        "model": role,
        "messages": [
            {"role": "system", "content": "Ты IT-помощник стажёра. Для действий используй инструменты."},
            {"role": "user", "content": TOOL_PROMPT},
        ],
        "tools": [SUGGEST_ACCESS_TOOL],
        "tool_choice": "auto",
    }, timeout)
    if status != 200:
        return False, f"HTTP {status}: {payload.get('error', {}).get('message', payload)}"
    calls = payload["choices"][0]["message"].get("tool_calls") or []
    if not calls:
        return False, "модель не вызвала инструмент"
    call = calls[0]["function"]
    if call.get("name") != "draft_access_request":
        return False, f"вызван не тот инструмент: {call.get('name')}"
    try:
        args = json.loads(call.get("arguments") or "")
    except ValueError:
        return False, f"arguments не JSON: {call.get('arguments')!r}"
    problems = []
    if args.get("service") != "grafana":
        problems.append(f"service={args.get('service')!r}, ждали 'grafana'")
    if args.get("level") != "write":
        problems.append(f"level={args.get('level')!r}, ждали 'write'")
    if not args.get("justification"):
        problems.append("пустое justification")
    if problems:
        return False, "; ".join(problems)
    return True, f"{took:.1f}s, {json.dumps(args, ensure_ascii=False)}"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--url", default="http://127.0.0.1:4001", help="адрес llm-gateway")
    parser.add_argument("--runs", type=int, default=1, help="сколько раз повторить проверку инструмента")
    parser.add_argument("--timeout", type=float, default=90)
    args = parser.parse_args()
    url = args.url.rstrip("/") + "/v1/chat/completions"

    checks = [(role, f"инструмент #{i + 1}", check_tool) for role in TOOL_ROLES for i in range(args.runs)]
    checks += [(role, "текст", check_text) for role in TEXT_ROLES]
    failed = 0
    for role, name, check in checks:
        ok, detail = check(url, role, args.timeout)
        failed += not ok
        print(f"{'OK  ' if ok else 'FAIL'} {role:<9} {name:<13} {detail}")
    print("\nвсё работает" if not failed else f"\nупало проверок: {failed}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
