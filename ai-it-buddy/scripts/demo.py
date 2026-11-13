#!/usr/bin/env python3
"""Демо-сценарии AI IT-Buddy через HTTP API ядра (без интерфейса). Только стандартная библиотека.

    python3 scripts/demo.py                    # все сценарии
    python3 scripts/demo.py access incident    # выбранные
    python3 scripts/demo.py --url http://127.0.0.1:8080 --demo-token demo

Сценарии (каждый тратит 2-3 запроса к LLM):
  access     стажер описывает задачу -> агент готовит заявку -> стажер подтверждает -> владелец согласует
  notbyrole  стажер просит прод -> "не по роли", решение наставника -> отказ DevOps с причиной
  incident   VPN лежит -> "это не у тебя, это инцидент", тикет не создается
  equipment  монитора нет на складе -> альтернативы
  onboarding путь адаптации: что сделано, следующий шаг
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:8080"
DEMO_TOKEN = "demo"


def call(method: str, path: str, token: str = "", body: dict | None = None, headers: dict | None = None) -> tuple[int, dict]:
    req = urllib.request.Request(BASE + path, method=method, data=json.dumps(body).encode() if body is not None else None)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=180) as resp:
            return resp.status, json.load(resp)
    except urllib.error.HTTPError as err:
        return err.code, json.load(err)


def login(user: str) -> str:
    code, out = call("POST", "/api/auth/login", body={"user_id": user})
    if code != 200:
        sys.exit(f"login {user}: {code} {out}")
    return out["token"]


def say(who: str, text: str) -> None:
    print(f"\n  {who}: {text}")


def chat(token: str, message: str, thread: str) -> dict:
    say("стажер", message)
    code, out = call("POST", "/api/chat", token, {"message": message, "thread_id": thread})
    if code != 200:
        say("ошибка", f"{code} {out.get('error')}")
        return {"drafts": []}
    say("buddy", out["reply"])
    print(f"    инструменты: {', '.join(out['tools_used']) or '-'}")
    for d in out["drafts"]:
        verdict = d["policy"]["verdict"]
        reasons = "; ".join(d["policy"].get("reasons", []))
        print(f"    карточка: {d['id']} {d['title']} [{d['status']}] согласует: {d.get('approver_name', '-')}"
              + (f"\n      НЕ ПО РОЛИ: {reasons}" if verdict != "ok" else ""))
    return out


def wait_status(token: str, rid: str, statuses: set[str], timeout: float = 60) -> dict:
    deadline = time.time() + timeout
    while time.time() < deadline:
        _, r = call("GET", f"/api/requests/{rid}", token)
        if r.get("status") in statuses:
            return r
        time.sleep(1)
    return r


def access() -> None:
    print("\n=== Доступы: задача вместо названия сервиса ===")
    anna = login("anna.intern")
    out = chat(anna, "Мне дали задачу поправить алерт в дашборде платежей, что нужно?", "demo-access")
    if not out["drafts"]:
        return
    rid = out["drafts"][0]["id"]
    code, r = call("POST", f"/api/requests/{rid}/confirm", anna)
    say("интерфейс", f"Анна нажала Подтвердить -> {r.get('status')} (согласует {r.get('approver_name')})")
    r = wait_status(anna, rid, {"approved", "rejected"})
    say("система", f"{rid}: {r['status']}. История: " + " -> ".join(e["status"] for e in r["history"]))


def not_by_role() -> None:
    print("\n=== Не по роли: решает наставник, причина отказа видна стажеру ===")
    anna, igor = login("anna.intern"), login("igor.mentor")
    out = chat(anna, "Дай мне доступ к продовому кластеру, хочу посмотреть поды", "demo-prod")
    drafts = [d for d in out["drafts"] if "Kubernetes" in d["title"]]
    if not drafts:
        say("demo", "модель не создала черновик на прод - на живой модели это зависит от формулировки")
        return
    rid = drafts[0]["id"]
    _, r = call("POST", f"/api/requests/{rid}/confirm", anna)
    say("интерфейс", f"Анна все равно отправила -> {r['status']}, ждет решения {r['approver_name']}")
    _, q = call("GET", "/api/approvals", igor)
    say("наставник", f"в очереди согласования: {[a['id'] for a in q['approvals']]}")
    _, r = call("POST", f"/api/approvals/{rid}/approve", igor, {"comment": "ок, пусть DevOps решит"})
    r = wait_status(anna, rid, {"approved", "rejected"})
    say("система", f"{rid}: {r['status']} - {r.get('resolution', '')}")


def incident() -> None:
    print("\n=== Это не у тебя, это инцидент ===")
    call("POST", "/api/demo/incidents", body={"service": "vpn", "title": "Недоступен шлюз VPN в Москве", "active": True},
         headers={"X-Demo-Token": DEMO_TOKEN})
    anna = login("anna.intern")
    chat(anna, "У меня не подключается VPN, помоги", "demo-vpn")
    call("POST", "/api/demo/incidents", body={"service": "vpn", "active": False}, headers={"X-Demo-Token": DEMO_TOKEN})


def equipment() -> None:
    print("\n=== Оборудование: нет на складе ===")
    chat(login("anna.intern"), "Мне нужен второй монитор 4K", "demo-eq")


def onboarding() -> None:
    print("\n=== Путь адаптации ===")
    anna = login("anna.intern")
    _, ob = call("GET", "/api/onboarding", anna)
    print(f"  сделано {ob['done']} из {ob['total']}, следующий шаг: {ob['next']['title']} ({ob['next']['hint']})")
    for s in ob["steps"]:
        print(f"    [{'x' if s['done'] else ' '}] неделя {s['week']}: {s['title']}")


SCENARIOS = {"access": access, "notbyrole": not_by_role, "incident": incident, "equipment": equipment, "onboarding": onboarding}


def main() -> None:
    global BASE, DEMO_TOKEN
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("scenarios", nargs="*", help=", ".join(SCENARIOS))
    p.add_argument("--url", default=BASE)
    p.add_argument("--demo-token", default=DEMO_TOKEN)
    args = p.parse_args()
    BASE, DEMO_TOKEN = args.url.rstrip("/"), args.demo_token
    unknown = [n for n in args.scenarios if n not in SCENARIOS]
    if unknown:
        p.error(f"unknown scenarios {unknown}, available: {', '.join(SCENARIOS)}")
    for name in args.scenarios or SCENARIOS:
        SCENARIOS[name]()
    print()


if __name__ == "__main__":
    main()
