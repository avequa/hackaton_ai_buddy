"""Проверка ответа перед отправкой: модель не должна обещать черновики, которых не создала.

Бесплатные модели иногда пишут «я подготовил черновики» после list_services, так и не вызвав draft_*.
Стажер ищет карточку, которой нет. Поэтому финальный ответ сверяется с тем, что реально вернули инструменты:
утверждение о черновике без созданного черновика - модель получает одну попытку исправиться,
потом ответ заменяется честным.
"""

from __future__ import annotations

import json
import re
from typing import Any

from langchain_core.messages import BaseMessage, ToolMessage

# «подготовил черновики», «создала заявку», «оформил обращение», «завел тикет»
_CLAIM_ACTION = re.compile(
    r"(подготовил\w*|создал\w*|оформил\w*|завел\w*|завёл|составил\w*|сделал\w*|отправил\w*)"
    r"[^.!?\n]{0,60}(черновик|заявк|тикет|обращени)",
    re.IGNORECASE,
)
# «черновик готов», «заявка создана»
_CLAIM_STATE = re.compile(r"(черновик\w*|заявк\w*)[^.!?\n]{0,30}(готов|создан|подготовлен|оформлен)", re.IGNORECASE)
# «нажми(те) Подтвердить» - имеет смысл, только если есть что подтверждать
_CLAIM_CONFIRM = re.compile(r"нажм\w*[^.!?\n]{0,30}подтверд", re.IGNORECASE)
_REQUEST_ID = re.compile(r"\b(?:ACC|HD|INV)-\d+\b")
_DRAFT_OUTCOMES = {"draft_created", "already_requested"}

RETRY_HINT = (
    "Проверка перед отправкой: в ответе сказано, что черновик или заявка подготовлены, но ни один инструмент "
    "draft_* в этом ходе черновик не создал. Вызови нужный draft_* сейчас (для доступа - draft_access_request "
    "на каждый недостающий доступ) или, если создать нельзя, ответь без утверждения о черновике. "
    "Номер черновика бери только из ответа инструмента."
)

HONEST_FALLBACK = (
    "Не получилось подготовить черновик автоматически. Напиши, какой сервис и уровень доступа нужен "
    "(например, «Grafana, write, поправить алерт»), и я оформлю черновик."
)


def tool_payload(msg: ToolMessage) -> Any:
    """JSON из ответа MCP-инструмента: content - строка или список текстовых блоков."""
    content = msg.content
    if isinstance(content, list):
        content = "".join(p.get("text", "") if isinstance(p, dict) else str(p) for p in content)
    try:
        return json.loads(content)
    except (TypeError, ValueError):
        return None


def drafts_in(messages: list[BaseMessage]) -> list[str]:
    """Номера черновиков, которые draft_* вернули в этих сообщениях (созданные или уже существующие)."""
    ids: list[str] = []
    for msg in messages:
        if not isinstance(msg, ToolMessage) or not (msg.name or "").startswith("draft_"):
            continue
        data = tool_payload(msg)
        if isinstance(data, dict) and data.get("outcome") in _DRAFT_OUTCOMES:
            req = data.get("request") or {}
            if isinstance(req, dict) and req.get("id"):
                ids.append(str(req["id"]))
    return ids


def known_ids(messages: list[BaseMessage]) -> set[str]:
    """Все номера заявок, которые хоть раз приходили от инструментов в этом треде."""
    found: set[str] = set()
    for msg in messages:
        if isinstance(msg, ToolMessage):
            text = msg.content if isinstance(msg.content, str) else json.dumps(msg.content, ensure_ascii=False)
            found.update(_REQUEST_ID.findall(text))
    return found


def claims_draft(text: str) -> bool:
    """В тексте утверждается, что черновик или заявка уже есть."""
    return bool(_CLAIM_ACTION.search(text) or _CLAIM_STATE.search(text) or _CLAIM_CONFIRM.search(text))


def unbacked_claim(text: str, turn: list[BaseMessage], thread: list[BaseMessage]) -> bool:
    """Ответ говорит о черновике, которого инструменты не подтверждают.

    Подтверждением считается черновик, созданный в этом ходе, или упоминание только тех номеров,
    которые раньше вернули инструменты (стажер спрашивает про вчерашний черновик).
    """
    if not claims_draft(text) or drafts_in(turn):
        return False
    mentioned = set(_REQUEST_ID.findall(text))
    return not mentioned or not mentioned <= known_ids(thread)
