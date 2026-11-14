"""Agent settings from environment variables."""

from __future__ import annotations

import os
from dataclasses import dataclass

MCP_SERVERS = ("access", "helpdesk", "inventory", "onboarding")


@dataclass(frozen=True)
class Settings:
    """Where the LLM gateway and the core MCP servers live, and how much context to keep."""

    llm_base_url: str = "http://llm-gateway:4000/v1"
    llm_model: str = "main"
    core_mcp_url: str = "http://core:8080/mcp"
    llm_timeout: float = 90.0
    max_steps: int = 18  # graph steps per turn (agent, tools, guard): about 8 LLM calls max, protects the free quota
    history_messages: int = 16  # how many recent messages of the thread go to the model

    @classmethod
    def from_env(cls, env: dict[str, str] | None = None) -> Settings:
        """Read settings; unset variables keep the defaults for docker compose."""
        env = dict(os.environ) if env is None else env
        d = cls()
        return cls(
            llm_base_url=env.get("LLM_BASE_URL", d.llm_base_url),
            llm_model=env.get("LLM_MODEL", d.llm_model),
            core_mcp_url=env.get("CORE_MCP_URL", d.core_mcp_url).rstrip("/"),
            llm_timeout=float(env.get("LLM_TIMEOUT", d.llm_timeout)),
            max_steps=int(env.get("AGENT_MAX_STEPS", d.max_steps)),
            history_messages=int(env.get("AGENT_HISTORY_MESSAGES", d.history_messages)),
        )
