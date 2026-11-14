"""LangGraph: model node + tool node, loop until the model stops calling tools."""

from __future__ import annotations

from typing import Any

from langchain_core.language_models import BaseChatModel
from langchain_core.messages import (
    AIMessage,
    BaseMessage,
    HumanMessage,
    RemoveMessage,
    SystemMessage,
    trim_messages,
)
from langchain_core.tools import BaseTool
from langgraph.checkpoint.base import BaseCheckpointSaver
from langgraph.graph import END, START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode

from .guard import HONEST_FALLBACK, RETRY_HINT, unbacked_claim

GUARD_MARK = "buddy_guard"  # служебные сообщения проверки, не показываются как ход пользователя


class State(MessagesState):
    """Conversation state: messages, the system prompt of the current user, guard retries in this turn."""

    system: str
    guard_retries: int


def current_turn(messages: list[BaseMessage]) -> list[BaseMessage]:
    """Сообщения с последнего настоящего сообщения пользователя (служебные подсказки проверки не считаются)."""
    for i in range(len(messages) - 1, -1, -1):
        msg = messages[i]
        if isinstance(msg, HumanMessage) and not msg.additional_kwargs.get(GUARD_MARK):
            return messages[i:]
    return messages


def recent(messages: list[BaseMessage], limit: int) -> list[BaseMessage]:
    """Last `limit` messages, starting on a human turn so tool calls are never cut from their results.

    If the current turn alone is longer than the limit (many tool calls), the whole current turn is kept:
    the model must always see the user's question.
    """
    kept = trim_messages(
        messages,
        max_tokens=limit,
        token_counter=len,
        strategy="last",
        start_on="human",
        include_system=False,
        allow_partial=False,
    )
    if kept and not kept[0].additional_kwargs.get(GUARD_MARK):
        return kept
    return current_turn(messages)


def build_graph(
    llm: BaseChatModel,
    tools: list[BaseTool],
    checkpointer: BaseCheckpointSaver | None = None,
    history_messages: int = 16,
) -> Any:
    """Compile the agent graph. The model sees the system prompt and only the recent history."""
    model = llm.bind_tools(tools)

    async def agent(state: State) -> dict[str, list[BaseMessage]]:
        history = recent(state["messages"], history_messages)
        reply = await model.ainvoke([SystemMessage(state["system"]), *history])
        return {"messages": [reply]}

    async def guard(state: State) -> dict[str, Any]:
        """Финальный ответ без вызова инструментов: проверить, что обещанные черновики существуют."""
        messages = state["messages"]
        final = messages[-1]
        text = message_text(final)
        if not unbacked_claim(text, current_turn(messages), messages):
            return {"guard_retries": 0}
        if state.get("guard_retries", 0) < 1:
            hint = HumanMessage(RETRY_HINT, additional_kwargs={GUARD_MARK: True})
            return {"messages": [RemoveMessage(id=final.id), hint], "guard_retries": 1}
        return {"messages": [RemoveMessage(id=final.id), AIMessage(HONEST_FALLBACK)], "guard_retries": 0}

    def after_agent(state: State) -> str:
        last = state["messages"][-1]
        return "tools" if isinstance(last, AIMessage) and last.tool_calls else "guard"

    def after_guard(state: State) -> str:
        last = state["messages"][-1]
        return "agent" if isinstance(last, HumanMessage) else END

    graph = StateGraph(State)
    graph.add_node("agent", agent)
    graph.add_node("tools", ToolNode(tools, handle_tool_errors=True))
    graph.add_node("guard", guard)
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", after_agent, ["tools", "guard"])
    graph.add_edge("tools", "agent")
    graph.add_conditional_edges("guard", after_guard, ["agent", END])
    return graph.compile(checkpointer=checkpointer)


def message_text(msg: BaseMessage) -> str:
    """Текст сообщения; некоторые провайдеры отдают content списком блоков."""
    text = msg.content
    if isinstance(text, list):
        text = "".join(part.get("text", "") if isinstance(part, dict) else str(part) for part in text)
    return str(text)


def turn_result(messages: list[BaseMessage], turn_start: int) -> tuple[str, list[str]]:
    """Final answer of the turn and the tools the model called during it."""
    tools_used: list[str] = []
    for msg in messages[turn_start:]:
        if isinstance(msg, AIMessage):
            tools_used.extend(call["name"] for call in msg.tool_calls)
    final = messages[-1] if messages else None
    text = message_text(final) if isinstance(final, AIMessage) else ""
    return text.strip(), tools_used
