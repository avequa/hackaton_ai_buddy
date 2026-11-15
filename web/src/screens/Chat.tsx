import { useEffect, useRef, useState, type FormEvent } from "react";
import { STATUS_LABEL } from "../api";
import { Icon } from "../Icon";
import { useCtx, type ModuleId } from "../context";
import {
  KIND_ICON,
  STATUS_TONE,
  api,
  errText,
  firstName,
  fmtTime,
  initials,
  isOpen,
  toolLabel,
} from "../lib";
import { Notes, RequestBody, StatusPill, Topbar } from "../parts";
import type { useChat } from "../useChat";
import { modules } from "./Modules";
import { Markdown } from "../Markdown";
import { BRAND_NAME } from "../Brand";

type Chat = ReturnType<typeof useChat>;

export function ChatScreen({
  activeModule,
  setActiveModule,
  chat,
}: {
  activeModule: ModuleId;
  setActiveModule: (id: ModuleId) => void;
  chat: Chat;
}) {
  const { user, requests } = useCtx();
  const [input, setInput] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);
  const current = modules.find((m) => m.id === activeModule) ?? modules[0];

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [chat.messages.length, chat.sending]);

  function submit(event: FormEvent) {
    event.preventDefault();
    const text = input.trim();
    if (!text || chat.sending) return;
    setInput("");
    void chat.send(text);
  }

  function ask(text: string) {
    if (chat.sending) return;
    void chat.send(text);
  }

  const userInitials = initials(user.name);

  return (
    <main className="content-area chat-screen">
      <Topbar title="Чат с Buddy" subtitle="Один помощник для всех рабочих вопросов" />
      <div className="chat-layout">
        <section className="chat-main">
          <div className="module-tabs">
            {modules.map((module) => (
              <button
                key={module.id}
                className={activeModule === module.id ? "active" : ""}
                onClick={() => setActiveModule(module.id)}
              >
                <Icon name={module.icon} size={17} /> {module.title}
              </button>
            ))}
            <button
              className="new-chat"
              onClick={chat.reset}
              disabled={chat.messages.length === 0}
              title="Начать новый диалог"
            >
              <Icon name="plus" size={15} /> Новый диалог
            </button>
          </div>

          <div className="conversation">
            <div className="day-separator">
              <span>Сегодня</span>
            </div>

            <div className="message assistant-message">
              <span className="bot-avatar">
                <Icon name="spark" size={18} />
              </span>
              <div>
                <small>{BRAND_NAME}</small>
                <div className="bubble">
                  Привет, {firstName(user.name)}! Я помогу с доступами, техникой и IT-вопросами. Что
                  нужно сделать?
                </div>
                {chat.messages.length === 0 && (
                  <div className="prompt-chips">
                    {current.prompts.map((p) => (
                      <button key={p} onClick={() => ask(p)} disabled={chat.sending}>
                        {p}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            </div>

            {chat.messages.map((m) =>
              m.role === "user" ? (
                <div className="message user-message" key={m.id}>
                  <div>
                    <small>Вы · {fmtTime(m.at)}</small>
                    <div className="bubble pre">{m.text}</div>
                  </div>
                  <span className="user-avatar">{userInitials}</span>
                </div>
              ) : (
                <div className="message assistant-message" key={m.id}>
                  <span className="bot-avatar">
                    <Icon name={m.drafts?.length ? "file" : "spark"} size={18} />
                  </span>
                  <div className="wide-message">
                    <small>{BRAND_NAME} · {fmtTime(m.at)}</small>
                    <div className={`bubble ${m.error ? "error pre" : ""}`}>{m.error ? m.text : <Markdown text={m.text} />}</div>
                    {m.drafts?.map((id) => {
                      const req = requests.find((r) => r.id === id);
                      return req ? <DraftCard key={id} id={id} /> : null;
                    })}
                    {m.tools && m.tools.length > 0 && (
                      <div className="source-line">
                        <Icon name="shield" size={14} /> Помощник: {m.tools.map(toolLabel).join(", ")}
                      </div>
                    )}
                  </div>
                </div>
              ),
            )}

            {chat.sending && (
              <div className="message assistant-message">
                <span className="bot-avatar">
                  <Icon name="spark" size={18} />
                </span>
                <div>
                  <small>{BRAND_NAME} печатает…</small>
                  <div className="bubble typing" aria-label="Помощник готовит ответ">
                    <i />
                    <i />
                    <i />
                  </div>
                </div>
              </div>
            )}
            <div ref={bottomRef} />
          </div>

          <form className="composer" onSubmit={submit}>
            <input
              value={input}
              onChange={(event) => setInput(event.target.value)}
              placeholder="Опишите задачу или задайте вопрос..."
              maxLength={4000}
              autoFocus
            />
            <span>Enter — отправить</span>
            <button className="send-button" aria-label="Отправить" disabled={chat.sending || !input.trim()}>
              <Icon name="send" size={18} />
            </button>
          </form>
        </section>
        <ContextPanel />
      </div>
    </main>
  );
}

/** Карточка заявки в чате: статус берётся из общего списка, поэтому обновляется сам при опросе */
function DraftCard({ id }: { id: string }) {
  const { requests, upsertRequest, refreshRequests, notify, openRequest } = useCtx();
  const [busy, setBusy] = useState<"confirm" | "cancel" | null>(null);
  const req = requests.find((r) => r.id === id);
  if (!req) return null;

  const isDraft = req.status === "draft";

  async function act(kind: "confirm" | "cancel") {
    setBusy(kind);
    try {
      const updated = kind === "confirm" ? await api.confirm(id) : await api.cancel(id);
      upsertRequest(updated);
    } catch (e) {
      notify(errText(e));
      void refreshRequests();
    } finally {
      setBusy(null);
    }
  }

  const title =
    req.status === "cancelled"
      ? "Заявка отменена"
      : req.status === "rejected"
        ? `Заявка ${req.id}: отказ`
        : `Заявка ${req.id} отправлена`;

  return (
    <div className="draft-card">
      <div className="draft-head">
        <div>
          <span className="draft-icon">
            <Icon name={KIND_ICON[req.kind]} size={19} />
          </span>
          <span>
            <small>{isDraft ? "ЧЕРНОВИК ЗАЯВКИ" : `ЗАЯВКА ${req.id}`}</small>
            <strong>{req.title}</strong>
          </span>
        </div>
        <StatusPill status={req.status} />
      </div>
      <RequestBody req={req} />
      <Notes req={req} compact />
      {isDraft ? (
        <div className="draft-actions">
          <button className="primary-button" disabled={busy !== null} onClick={() => act("confirm")}>
            <Icon name="check" size={17} /> {busy === "confirm" ? "Отправляем…" : "Подтвердить и отправить"}
          </button>
          <button className="secondary-button" disabled={busy !== null} onClick={() => act("cancel")}>
            {busy === "cancel" ? "Отменяем…" : "Отменить"}
          </button>
        </div>
      ) : (
        <div className={`success-action ${STATUS_TONE[req.status]}`}>
          <span>
            <Icon name={req.status === "cancelled" || req.status === "rejected" ? "close" : "check"} size={17} />
          </span>
          <div>
            <strong>{title}</strong>
            <small>Статус: {STATUS_LABEL[req.status].toLowerCase()}</small>
          </div>
          <button onClick={() => openRequest(req.id)}>Открыть</button>
        </div>
      )}
    </div>
  );
}

function ContextPanel() {
  const { user, onboarding, requests, nameOf, openRequest, go, approvalsCount } = useCtx();
  const open = requests.filter(isOpen);

  // Окно из 5 шагов вокруг текущего
  let steps = onboarding?.steps ?? [];
  let offset = 0;
  if (onboarding && steps.length > 5) {
    const firstUndone = steps.findIndex((s) => !s.done);
    const from = Math.max(0, Math.min((firstUndone === -1 ? steps.length : firstUndone) - 2, steps.length - 5));
    offset = from;
    steps = steps.slice(from, from + 5);
  }
  const pct = onboarding && onboarding.total > 0 ? Math.round((onboarding.done / onboarding.total) * 100) : 0;
  const mentorName = user.mentor ? nameOf(user.mentor) : null;

  return (
    <aside className="context-panel">
      {onboarding && (
        <>
          <div className="context-head">
            <div>
              <p>Ваш путь</p>
              <h3>Адаптация</h3>
            </div>
            <span>
              {onboarding.done} из {onboarding.total}
            </span>
          </div>
          <div className="progress-track">
            <i style={{ width: `${pct}%` }} />
          </div>
          <div className="checklist">
            {steps.map((s, i) => {
              const isNext = onboarding.next?.id === s.id;
              return (
                <div key={s.id} className={s.done ? "done" : isNext ? "current" : ""}>
                  <span>{s.done ? <Icon name="check" size={14} /> : offset + i + 1}</span>
                  <div>
                    <strong>{s.title}</strong>
                    <small>{s.done ? "Завершено" : isNext ? "Текущий шаг" : `Неделя ${s.week}`}</small>
                  </div>
                </div>
              );
            })}
          </div>
          <button className="panel-link" onClick={() => go("onboarding")}>
            Весь путь адаптации <Icon name="arrow" size={14} />
          </button>
        </>
      )}

      {user.role !== "intern" && (
        <button className="mini-request approvals-tile" onClick={() => go("approvals")}>
          <span className="mini-icon violet">
            <Icon name="check" size={16} />
          </span>
          <span>
            <strong>Согласования</strong>
            <small>{approvalsCount > 0 ? `Ждут решения: ${approvalsCount}` : "Очередь пуста"}</small>
          </span>
        </button>
      )}

      <div className="recent-title">
        <h3>Активные заявки</h3>
        <button onClick={() => go("requests")}>Все</button>
      </div>
      {open.length === 0 && <p className="panel-empty">Нет активных заявок</p>}
      {open.slice(0, 4).map((r) => (
        <button key={r.id} className="mini-request" onClick={() => openRequest(r.id)}>
          <span className={`mini-icon ${STATUS_TONE[r.status]}`}>
            <Icon name={KIND_ICON[r.kind]} size={16} />
          </span>
          <span>
            <strong>{r.id}</strong>
            <small>{r.title}</small>
          </span>
          <StatusPill status={r.status} />
        </button>
      ))}

      {mentorName && (
        <div className="mentor-note">
          <div className="mentor-avatar">{initials(mentorName)}</div>
          <div>
            <small>Ваш наставник</small>
            <strong>{mentorName}</strong>
          </div>
        </div>
      )}
    </aside>
  );
}
