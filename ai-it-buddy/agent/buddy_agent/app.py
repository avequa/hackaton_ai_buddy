"""HTTP entrypoint of the agent. Only the Go core calls it; the browser never talks to the agent directly."""

from __future__ import annotations

import asyncio
import logging
import threading
from collections.abc import Coroutine
from typing import Any

from flask import Flask, jsonify, request

from .config import Settings
from .service import Agent

log = logging.getLogger(__name__)

MAX_MESSAGE = 4000
TURN_TIMEOUT = 170  # секунд; ядро ждет агента 3 минуты


class LoopRunner:
    """One long-lived event loop in a background thread.

    Async HTTP clients (OpenAI, MCP) are cached and bound to the loop that created them, so asyncio.run()
    per request breaks the second request with "Event loop is closed". All turns run on this loop instead.
    """

    def __init__(self) -> None:
        self.loop = asyncio.new_event_loop()
        threading.Thread(target=self.loop.run_forever, name="agent-loop", daemon=True).start()

    def run(self, coro: Coroutine[Any, Any, Any], timeout: float = TURN_TIMEOUT) -> Any:
        """Run a coroutine on the shared loop from a Flask worker thread and wait for the result."""
        return asyncio.run_coroutine_threadsafe(coro, self.loop).result(timeout)


def create_app(agent: Any | None = None) -> Flask:
    """Flask app; the agent is injectable for tests."""
    app = Flask(__name__)
    buddy = agent or Agent(Settings.from_env())
    runner = LoopRunner()

    @app.get("/healthz")
    def healthz() -> tuple[str, int]:
        return "ok\n", 200

    @app.post("/chat")
    def chat() -> Any:
        body = request.get_json(silent=True)
        if not isinstance(body, dict):
            return jsonify(error="JSON body required"), 400
        message = str(body.get("message", "")).strip()
        thread_id, token = body.get("thread_id"), body.get("token")
        user = body.get("user") if isinstance(body.get("user"), dict) else {}
        if not message or len(message) > MAX_MESSAGE or not thread_id or not token:
            return jsonify(error="message (1..4000 chars), thread_id and token are required"), 400
        try:
            result = runner.run(buddy.chat(message, str(thread_id), str(token), user))
        except Exception:  # noqa: BLE001 - любую ошибку LLM или MCP превращаем в 502, подробности в лог
            log.exception("chat turn failed")
            return jsonify(error="agent failed, see agent logs"), 502
        return jsonify(result)

    return app


def main() -> None:
    """Local run without gunicorn: python -m buddy_agent.app"""
    logging.basicConfig(level=logging.INFO)
    create_app().run(host="0.0.0.0", port=8000)  # noqa: S104 - внутри контейнера


if __name__ == "__main__":  # pragma: no cover
    main()
