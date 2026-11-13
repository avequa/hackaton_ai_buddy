"""Fake chat model for tests: answers from a script, records what it was shown. No network, no LLM quota."""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

from langchain_core.callbacks import CallbackManagerForLLMRun
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import AIMessage, BaseMessage
from langchain_core.outputs import ChatGeneration, ChatResult

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


class ScriptedChat(BaseChatModel):
    """Returns the next scripted AIMessage on every call; bind_tools is a no-op."""

    script: list[AIMessage]
    seen: list[list[BaseMessage]] = []
    bound_tools: list[str] = []

    @property
    def _llm_type(self) -> str:
        return "scripted"

    def bind_tools(self, tools: Any, **_: Any) -> ScriptedChat:  # type: ignore[override]
        self.bound_tools = [t.name for t in tools]
        return self

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **_: Any,
    ) -> ChatResult:
        self.seen.append(list(messages))
        if not self.script:
            raise RuntimeError("script exhausted")
        return ChatResult(generations=[ChatGeneration(message=self.script.pop(0))])


def tool_call(name: str, args: dict[str, Any], call_id: str = "c1") -> AIMessage:
    """AIMessage that asks to call one tool."""
    return AIMessage(content="", tool_calls=[{"name": name, "args": args, "id": call_id, "type": "tool_call"}])
