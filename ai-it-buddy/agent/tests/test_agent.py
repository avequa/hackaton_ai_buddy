from __future__ import annotations

import asyncio
from typing import Any

import pytest
from conftest import ScriptedChat, tool_call
from langchain_core.messages import AIMessage, HumanMessage, SystemMessage, ToolMessage
from langchain_core.tools import ToolException, tool

from buddy_agent.app import create_app
from buddy_agent.config import Settings
from buddy_agent.graph import recent, turn_result
from buddy_agent.prompts import system_prompt
from buddy_agent.service import STEP_LIMIT_REPLY, Agent, default_llm

USER = {"id": "anna.intern", "name": "Анна", "role": "intern", "position": "backend", "team": "payments"}


@tool
def list_services() -> str:
    """Corporate services catalog."""
    return '{"services":[{"id":"grafana"}]}'


@tool
def draft_access_request(service: str, project: str, level: str, justification: str) -> str:
    """Creates a draft access request."""
    if service == "nope":
        raise ToolException("unknown service \"nope\", available: gitlab, grafana")
    return '{"outcome":"draft_created","request":{"id":"ACC-1001","status":"draft"}}'


def make_agent(script: list[AIMessage], tokens: list[str] | None = None) -> tuple[Agent, ScriptedChat]:
    llm = ScriptedChat(script=script, seen=[])

    async def loader(_: Settings, token: str) -> list[Any]:
        if tokens is not None:
            tokens.append(token)
        return [list_services, draft_access_request]

    return Agent(Settings(max_steps=12, history_messages=6), llm_factory=lambda _: llm, tools_loader=loader), llm


def test_turn_with_tools_and_memory() -> None:
    tokens: list[str] = []
    agent, llm = make_agent(
        [
            tool_call("list_services", {}),
            tool_call("draft_access_request", {"service": "grafana", "project": "payments-dashboards",
                                               "level": "write", "justification": "поправить алерт"}, "c2"),
            AIMessage(content="Подготовила черновик ACC-1001, проверь и нажми Подтвердить."),
            AIMessage(content="Статус черновика - draft."),
        ],
        tokens,
    )
    out = asyncio.run(agent.chat("нужно поправить алерт", "anna:t1", "tok-1", USER))
    assert out == {
        "reply": "Подготовила черновик ACC-1001, проверь и нажми Подтвердить.",
        "tools_used": ["list_services", "draft_access_request"],
    }
    assert llm.bound_tools == ["list_services", "draft_access_request"]
    first = llm.seen[0]
    assert isinstance(first[0], SystemMessage) and "команда payments" in first[0].content

    # второй ход того же треда помнит историю, а инструменты этого хода не смешиваются с прошлыми
    out = asyncio.run(agent.chat("какой статус?", "anna:t1", "tok-2", USER))
    assert out == {"reply": "Статус черновика - draft.", "tools_used": []}
    assert tokens == ["tok-1", "tok-2"]
    last_seen = llm.seen[-1]
    assert any(isinstance(m, HumanMessage) and m.content == "нужно поправить алерт" for m in last_seen) is False
    assert isinstance(last_seen[1], HumanMessage) and last_seen[1].content == "какой статус?"


def test_tool_error_goes_back_to_model() -> None:
    agent, llm = make_agent([
        tool_call("draft_access_request", {"service": "nope", "project": "x", "level": "read", "justification": "x"}),
        AIMessage(content="Такого сервиса нет, есть gitlab и grafana."),
    ])
    out = asyncio.run(agent.chat("доступ к nope", "t2", "tok", USER))
    assert out["reply"].startswith("Такого сервиса нет")
    tool_msgs = [m for m in llm.seen[1] if isinstance(m, ToolMessage)]
    assert tool_msgs and "unknown service" in tool_msgs[0].content


def test_step_limit() -> None:
    agent, _ = make_agent([tool_call("list_services", {}, f"c{i}") for i in range(20)])
    out = asyncio.run(agent.chat("зациклись", "t3", "tok", USER))
    assert out == {"reply": STEP_LIMIT_REPLY, "tools_used": []}


def test_empty_reply_fallback() -> None:
    agent, _ = make_agent([AIMessage(content=[{"type": "text", "text": "  "}])])
    out = asyncio.run(agent.chat("?", "t4", "tok", USER))
    assert "переформулировать" in out["reply"]


def test_recent_keeps_tool_pairs() -> None:
    msgs = [
        HumanMessage("1"), AIMessage(content="a"),
        HumanMessage("2"), tool_call("list_services", {}), ToolMessage("r", tool_call_id="c1"), AIMessage(content="b"),
    ]
    assert [m.content for m in recent(msgs, 4)][0] == "2"
    # текущий ход длиннее лимита: он остается целиком, вопрос пользователя не теряется
    assert recent(msgs, 2)[0].content == "2" and len(recent(msgs, 2)) == 4
    assert recent([AIMessage(content="x")], 0) == [AIMessage(content="x")]


def test_load_mcp_tools(monkeypatch: pytest.MonkeyPatch) -> None:
    import buddy_agent.service as service

    seen: dict[str, Any] = {}

    class FakeClient:
        def __init__(self, connections: dict[str, Any]) -> None:
            seen.update(connections)

        async def get_tools(self) -> list[str]:
            return ["t"]

    monkeypatch.setattr(service, "MultiServerMCPClient", FakeClient)
    tools = asyncio.run(service.load_mcp_tools(Settings(core_mcp_url="http://core:8080/mcp"), "tok"))
    assert tools == ["t"]
    assert sorted(seen) == ["access", "helpdesk", "inventory", "onboarding"]
    assert seen["access"]["url"] == "http://core:8080/mcp/access"
    assert seen["access"]["headers"] == {"Authorization": "Bearer tok"}


def test_turn_result_content_blocks() -> None:
    msgs = [HumanMessage("q"), AIMessage(content=[{"type": "text", "text": "ок "}, "!"])]
    assert turn_result(msgs, 0) == ("ок !", [])
    assert turn_result([], 0) == ("", [])


def test_prompt_and_settings() -> None:
    assert "Анна" in system_prompt(USER) and "коллега" in system_prompt({})
    s = Settings.from_env({"CORE_MCP_URL": "http://core:9/mcp/", "AGENT_MAX_STEPS": "5", "LLM_TIMEOUT": "3"})
    assert s.core_mcp_url == "http://core:9/mcp" and s.max_steps == 5 and s.llm_timeout == 3.0
    assert Settings.from_env({}).llm_model == "main"
    llm = default_llm(Settings())
    assert llm.model_name == "main"


class FakeAgent:
    def __init__(self, fail: bool = False) -> None:
        self.fail = fail
        self.calls: list[tuple[str, str, str, dict[str, Any]]] = []

    async def chat(self, message: str, thread_id: str, token: str, user: dict[str, Any]) -> dict[str, Any]:
        if self.fail:
            raise RuntimeError("llm down")
        self.calls.append((message, thread_id, token, user))
        return {"reply": "ок", "tools_used": ["search_kb"]}


@pytest.mark.parametrize(
    ("body", "code"),
    [
        (None, 400),
        ({"message": "", "thread_id": "t", "token": "x"}, 400),
        ({"message": "привет", "thread_id": "t"}, 400),
        ({"message": "x" * 4001, "thread_id": "t", "token": "x"}, 400),
        ({"message": "привет", "thread_id": "t", "token": "x", "user": USER}, 200),
    ],
)
def test_http_chat(body: dict[str, Any] | None, code: int) -> None:
    fake = FakeAgent()
    client = create_app(fake).test_client()
    resp = client.post("/chat", json=body) if body is not None else client.post("/chat", data="nope")
    assert resp.status_code == code
    if code == 200:
        assert resp.get_json() == {"reply": "ок", "tools_used": ["search_kb"]}
        assert fake.calls[0][1:3] == ("t", "x")


def test_http_many_turns_same_loop() -> None:
    import asyncio as aio

    loops: set[int] = set()

    class LoopAgent(FakeAgent):
        async def chat(self, message: str, thread_id: str, token: str, user: dict[str, Any]) -> dict[str, Any]:
            loops.add(id(aio.get_running_loop()))
            return {"reply": "ок", "tools_used": []}

    client = create_app(LoopAgent()).test_client()
    for _ in range(3):
        assert client.post("/chat", json={"message": "привет", "thread_id": "t", "token": "x"}).status_code == 200
    assert len(loops) == 1, "all turns must run on one long-lived loop"


def test_http_errors_and_health() -> None:
    client = create_app(FakeAgent(fail=True)).test_client()
    assert client.get("/healthz").status_code == 200
    resp = client.post("/chat", json={"message": "привет", "thread_id": "t", "token": "x"})
    assert resp.status_code == 502 and "agent failed" in resp.get_json()["error"]


def test_guard_forces_draft_when_model_only_promises() -> None:
    """Модель пишет «подготовил черновики», не вызвав draft_* - получает подсказку и вызывает инструмент."""
    agent, llm = make_agent([
        tool_call("list_services", {}),
        AIMessage(content="Я подготовил черновики заявок. Проверьте карточки и нажмите «Подтвердить»."),
        tool_call("draft_access_request", {"service": "grafana", "project": "payments-dashboards",
                                           "level": "write", "justification": "алерт"}, "c2"),
        AIMessage(content="Подготовил черновик ACC-1001: Grafana, write. Нажми «Подтвердить» в карточке."),
    ])
    out = asyncio.run(agent.chat("поправить алерт", "g1", "tok", USER))
    assert out["reply"].startswith("Подготовил черновик ACC-1001")
    assert out["tools_used"] == ["list_services", "draft_access_request"]
    hint = llm.seen[2][-1]
    assert isinstance(hint, HumanMessage) and "draft_access_request" in hint.content
    # ложное обещание удалено из истории: следующий ход его не видит
    assert all("Я подготовил черновики" not in str(m.content) for m in llm.seen[3])


def test_guard_replaces_second_false_claim_with_honest_answer() -> None:
    agent, _ = make_agent([
        AIMessage(content="Заявка создана, жди."),
        AIMessage(content="Черновик готов, нажми Подтвердить."),
    ])
    out = asyncio.run(agent.chat("дай доступ", "g2", "tok", USER))
    assert out["reply"].startswith("Не получилось подготовить черновик автоматически")
    # следующий ход начинается нормально, счетчик попыток сброшен
    agent2, _ = make_agent([AIMessage(content="Привет! Чем помочь?")])
    assert asyncio.run(agent2.chat("привет", "g3", "tok", USER))["reply"] == "Привет! Чем помочь?"


def test_guard_allows_reference_to_earlier_draft() -> None:
    agent, _ = make_agent([
        tool_call("draft_access_request", {"service": "grafana", "project": "p", "level": "read", "justification": "x"}),
        AIMessage(content="Подготовил черновик ACC-1001."),
        AIMessage(content="Черновик ACC-1001 готов, нажми «Подтвердить» в карточке."),
    ])
    asyncio.run(agent.chat("доступ", "g4", "tok", USER))
    out = asyncio.run(agent.chat("а что дальше?", "g4", "tok", USER))
    assert out["reply"] == "Черновик ACC-1001 готов, нажми «Подтвердить» в карточке."


def test_guard_helpers() -> None:
    from buddy_agent.guard import claims_draft, drafts_in, tool_payload

    assert claims_draft("Я подготовил черновики заявок на недостающие доступы")
    assert claims_draft("Проверьте карточки заявок в интерфейсе и нажмите «Подтвердить»")
    assert not claims_draft("Доступ к Grafana у тебя уже есть, заявка не нужна.")
    blocks = ToolMessage(content=[{"type": "text", "text": '{"outcome":"already_requested","request":{"id":"ACC-7"}}'}],
                         tool_call_id="x", name="draft_access_request")
    assert drafts_in([blocks]) == ["ACC-7"]
    assert tool_payload(ToolMessage(content="not json", tool_call_id="y")) is None
    assert drafts_in([ToolMessage(content='{"outcome":"already_granted"}', tool_call_id="z", name="draft_access_request")]) == []
