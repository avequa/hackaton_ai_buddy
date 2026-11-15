import { useCallback, useEffect, useRef, useState } from "react";
import type { Request as Req } from "./api";
import { api, errText, newThreadId, uid } from "./lib";

export interface ChatMsg {
  id: string;
  role: "user" | "assistant";
  text: string;
  at: string;
  tools?: string[];
  /** id черновиков, показанных карточками под этим сообщением */
  drafts?: string[];
  error?: boolean;
}

interface ChatState {
  threadId: string;
  messages: ChatMsg[];
}

function load(key: string): ChatState {
  try {
    const raw = localStorage.getItem(key);
    if (raw) {
      const p = JSON.parse(raw) as Partial<ChatState>;
      if (typeof p.threadId === "string" && Array.isArray(p.messages)) {
        return { threadId: p.threadId, messages: p.messages };
      }
    }
  } catch {
    /* повреждённое хранилище - начнём заново */
  }
  return { threadId: newThreadId(), messages: [] };
}

/**
 * Состояние чата живёт на уровне приложения, а не экрана: запрос длится 3-15 с (бывает дольше),
 * пользователь может уйти на другой экран, и ответ не должен потеряться.
 * История на сервере хранится по thread_id, но получить её обратно API не позволяет,
 * поэтому копию сообщений держим в localStorage.
 */
export function useChat(userId: string, upsert: (r: Req) => void, afterTurn: () => void) {
  const key = `itbuddy.chat.${userId}`;
  const [state, setState] = useState<ChatState>(() => load(key));
  const [sending, setSending] = useState(false);
  const threadRef = useRef(state.threadId);
  threadRef.current = state.threadId;

  useEffect(() => {
    try {
      localStorage.setItem(key, JSON.stringify(state));
    } catch {
      /* квота или приватный режим - не критично */
    }
  }, [key, state]);

  const send = useCallback(
    async (text: string) => {
      const message = text.trim();
      if (!message) return;
      const tid = threadRef.current;
      const mine: ChatMsg = { id: uid(), role: "user", text: message, at: new Date().toISOString() };
      setState((s) => ({ ...s, messages: [...s.messages, mine] }));
      setSending(true);
      try {
        const res = await api.chat({ message, thread_id: tid });
        res.drafts.forEach(upsert);
        // Если пользователь успел начать новый диалог - ответ старого не показываем
        if (threadRef.current === tid) {
          const ids = res.drafts.map((d) => d.id);
          const reply: ChatMsg = {
            id: uid(),
            role: "assistant",
            text: res.reply,
            at: new Date().toISOString(),
            tools: res.tools_used,
            drafts: ids,
          };
          setState((s) => ({
            ...s,
            // drafts приходят целиком: прежние карточки тех же черновиков убираем, чтобы не дублировались
            messages: [
              ...s.messages.map((m) =>
                m.drafts ? { ...m, drafts: m.drafts.filter((id) => !ids.includes(id)) } : m,
              ),
              reply,
            ],
          }));
        }
        afterTurn();
      } catch (e) {
        if (threadRef.current === tid) {
          const err: ChatMsg = {
            id: uid(),
            role: "assistant",
            text: errText(e),
            at: new Date().toISOString(),
            error: true,
          };
          setState((s) => ({ ...s, messages: [...s.messages, err] }));
        }
      } finally {
        setSending(false);
      }
    },
    [upsert, afterTurn],
  );

  const reset = useCallback(() => {
    setState({ threadId: newThreadId(), messages: [] });
  }, []);

  return { messages: state.messages, sending, send, reset };
}
