"""LangGraph: model node + tool node, loop until the model stops calling tools."""

from __future__ import annotations

from typing import Any

from langchain_core.language_models import BaseChatModel
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, SystemMessage, trim_messages
from langchain_core.tools import BaseTool
from langgraph.checkpoint.base import BaseCheckpointSaver
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import ToolNode, tools_condition


class State(MessagesState):
    """Conversation state: messages plus the system prompt of the current user."""

    system: str


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
    if kept:
        return kept
    for i in range(len(messages) - 1, -1, -1):
        if isinstance(messages[i], HumanMessage):
            return messages[i:]
    return messages


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

    graph = StateGraph(State)
    graph.add_node("agent", agent)
    graph.add_node("tools", ToolNode(tools, handle_tool_errors=True))
    graph.add_edge(START, "agent")
    graph.add_conditional_edges("agent", tools_condition)
    graph.add_edge("tools", "agent")
    return graph.compile(checkpointer=checkpointer)


def turn_result(messages: list[BaseMessage], turn_start: int) -> tuple[str, list[str]]:
    """Final answer of the turn and the tools the model called during it."""
    tools_used: list[str] = []
    for msg in messages[turn_start:]:
        if isinstance(msg, AIMessage):
            tools_used.extend(call["name"] for call in msg.tool_calls)
    final = messages[-1] if messages else None
    text = final.content if isinstance(final, AIMessage) else ""
    if isinstance(text, list):  # some providers return content blocks
        text = "".join(part.get("text", "") if isinstance(part, dict) else str(part) for part in text)
    return text.strip(), tools_used
