"""One chat turn: connect to MCP with the user's short-lived token, run the graph, return the answer."""

from __future__ import annotations

import logging
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

from langchain_core.language_models import BaseChatModel
from langchain_core.messages import HumanMessage
from langchain_core.tools import BaseTool
from langchain_mcp_adapters.client import MultiServerMCPClient
from langchain_openai import ChatOpenAI
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.errors import GraphRecursionError

from .config import MCP_SERVERS, Settings
from .graph import build_graph, turn_result
from .prompts import system_prompt

log = logging.getLogger(__name__)

STEP_LIMIT_REPLY = (
    "Не получилось уложиться в разумное число шагов. Переформулируй вопрос короче "
    "или спроси наставника."
)


async def load_mcp_tools(settings: Settings, token: str) -> list[BaseTool]:
    """MCP tools of all core servers; every call carries the user's token, the server checks rights."""
    headers = {"Authorization": f"Bearer {token}"}
    client = MultiServerMCPClient(
        {
            name: {"url": f"{settings.core_mcp_url}/{name}", "transport": "streamable_http", "headers": headers}
            for name in MCP_SERVERS
        }
    )
    return await client.get_tools()


def default_llm(settings: Settings) -> BaseChatModel:
    """OpenAI-compatible client to llm-gateway: the agent asks for a role, the gateway picks the model."""
    return ChatOpenAI(
        base_url=settings.llm_base_url,
        api_key="unused",  # the key lives in the gateway, not in the agent
        model=settings.llm_model,
        timeout=settings.llm_timeout,
        max_retries=1,
    )


@dataclass
class Agent:
    """Agent with in-memory threads. Tools are loaded per turn because the token is per user and turn."""

    settings: Settings
    llm_factory: Callable[[Settings], BaseChatModel] = default_llm
    tools_loader: Callable[[Settings, str], Any] = load_mcp_tools
    checkpointer: InMemorySaver = field(default_factory=InMemorySaver)

    async def chat(self, message: str, thread_id: str, token: str, user: dict[str, str]) -> dict[str, Any]:
        """Run one turn and return {reply, tools_used}."""
        tools = await self.tools_loader(self.settings, token)
        graph = build_graph(self.llm_factory(self.settings), tools, self.checkpointer, self.settings.history_messages)
        config = {"configurable": {"thread_id": thread_id}, "recursion_limit": self.settings.max_steps}

        snapshot = await graph.aget_state(config)
        turn_start = len(snapshot.values.get("messages", [])) if snapshot and snapshot.values else 0
        try:
            state = await graph.ainvoke(
                {"messages": [HumanMessage(message)], "system": system_prompt(user)},
                config,
            )
        except GraphRecursionError:
            log.warning("step limit reached", extra={"thread_id": thread_id})
            return {"reply": STEP_LIMIT_REPLY, "tools_used": []}
        reply, tools_used = turn_result(state["messages"], turn_start)
        return {"reply": reply or "Не смог сформулировать ответ, попробуй переформулировать.", "tools_used": tools_used}
