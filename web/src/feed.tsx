import { useEffect, useRef, useState } from "react";
import { RADAR, type Notification } from "./api";
import { Icon } from "./Icon";
import { useCtx } from "./context";
import { LEVEL_LABEL, NOTE_META, api, errText, fmtAgo, serviceLabel } from "./lib";

/** Колокольчик в шапке: лента уведомлений. Открыли - все прочитаны. */
export function Bell() {
  const { feed, openNote } = useCtx();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    void feed.markAllRead();
    const close = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open, feed]);

  const list = [...feed.notes].reverse();
  return (
    <div className="bell" ref={ref}>
      <button className="icon-button bell-button" onClick={() => setOpen((v) => !v)} aria-label="Уведомления">
        <Icon name="bell" size={19} />
        {feed.unread > 0 && <b>{feed.unread > 99 ? "99+" : feed.unread}</b>}
      </button>
      {open && (
        <div className="bell-panel" role="dialog" aria-label="Уведомления">
          <div className="bell-head">
            <strong>Уведомления</strong>
            <small>{list.length ? `${list.length} последних` : ""}</small>
          </div>
          {list.length === 0 && <p className="bell-empty">Пока ничего нет</p>}
          <div className="bell-list">
            {list.map((n) => (
              <NoteRow
                key={n.id}
                note={n}
                onClick={() => {
                  setOpen(false);
                  openNote(n);
                }}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function NoteRow({ note, onClick }: { note: Notification; onClick: () => void }) {
  const meta = NOTE_META[note.kind] ?? NOTE_META.request;
  return (
    <button className={`note-row tone-${meta.tone} ${note.read ? "" : "unread"}`} onClick={onClick}>
      <span className="note-icon">
        <Icon name={meta.icon} size={16} />
      </span>
      <span className="note-body">
        <small>
          {meta.label} · {fmtAgo(note.created_at)}
        </small>
        <strong>{note.title}</strong>
        {note.text && <span>{note.text}</span>}
      </span>
    </button>
  );
}

/** Баннер открытых сбоев поверх любого экрана */
export function IncidentBanner() {
  const { feed } = useCtx();
  if (feed.openIncidents.length === 0 && !feed.degraded) return null;
  return (
    <div className="banners">
      {feed.openIncidents.map((inc) => (
        <div className="incident-banner" key={inc.id} role="alert">
          <span className="incident-icon">
            <Icon name="alert" size={18} />
          </span>
          <div>
            <strong>
              Сбой: {serviceLabel(inc.service)} — {inc.title}
            </strong>
            <p>
              {inc.message ? `${inc.message} ` : ""}Проблема не у вас: заявку в поддержку заводить не нужно, сообщим, когда
              починят.
            </p>
          </div>
          <small>
            {inc.id} · {inc.by === RADAR ? "обнаружено автоматически по логам" : "объявил дежурный"} · {fmtAgo(inc.since)}
          </small>
        </div>
      ))}
      {feed.degraded && (
        <div className="degraded-note">
          <Icon name="refresh" size={14} /> Сохраняем изменения… База данных временно недоступна, всё работает, данные
          допишутся автоматически.
        </div>
      )}
    </div>
  );
}

type Pack = Awaited<ReturnType<typeof api.starterPack>>;
const OUTCOME: Record<string, { text: string; tone: string }> = {
  draft_created: { text: "Подготовим заявку", tone: "blue" },
  already_granted: { text: "Уже есть", tone: "green" },
  already_requested: { text: "Уже запрошено", tone: "amber" },
};

/** Стартовый набор доступов: черновики на недостающее и подтверждение одной кнопкой */
export function StarterPackDialog({ onClose }: { onClose: () => void }) {
  const { refreshRequests, notify } = useCtx();
  const [pack, setPack] = useState<Pack | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<{ id: string; ok: boolean; text: string }[] | null>(null);

  useEffect(() => {
    api
      .starterPack()
      .then(setPack)
      .catch((e) => setError(errText(e)));
  }, []);

  async function confirmAll() {
    if (!pack) return;
    setBusy(true);
    try {
      const res = await api.confirmBatch(pack.draft_ids);
      setResults(
        res.results.map((r) => ({ id: r.id, ok: !r.error, text: r.error ?? r.request?.title ?? "отправлено" })),
      );
      const ok = res.results.filter((r) => !r.error).length;
      notify(`Отправлено заявок: ${ok} из ${res.results.length}`, ok ? "ok" : "error");
      await refreshRequests();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-label="Стартовый набор доступов">
        <div className="modal-head">
          <span className="draft-icon">
            <Icon name="key" size={19} />
          </span>
          <div>
            <strong>Стартовый набор доступов</strong>
            <small>Всё, что нужно в первый день: VPN, GitLab, Jira и Confluence вашей команды</small>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="Закрыть">
            <Icon name="close" size={18} />
          </button>
        </div>
        {error && <div className="login-error">{error}</div>}
        {!pack && !error && <div className="screen-loading">Проверяем, что у вас уже есть…</div>}
        {pack && !results && (
          <>
            <ul className="pack-list">
              {pack.items.map((it) => {
                const o = OUTCOME[it.outcome];
                return (
                  <li key={`${it.service}/${it.project}`}>
                    <span className="service-logo">{serviceLabel(it.service).slice(0, 2).toUpperCase()}</span>
                    <span>
                      <strong>{serviceLabel(it.service)}</strong>
                      <small>
                        {it.project} · {LEVEL_LABEL[it.level as keyof typeof LEVEL_LABEL] ?? it.level}
                      </small>
                    </span>
                    <span className={`status-pill ${o.tone}`}>{o.text}</span>
                  </li>
                );
              })}
            </ul>
            <div className="modal-actions">
              {pack.draft_ids.length > 0 ? (
                <>
                  <p>Заявки уйдут согласующим, статус увидите в «Моих заявках».</p>
                  <button className="primary-button" disabled={busy} onClick={confirmAll}>
                    <Icon name="check" size={17} /> {busy ? "Отправляем…" : `Подтвердить все (${pack.draft_ids.length})`}
                  </button>
                </>
              ) : (
                <>
                  <p>Все стандартные доступы уже есть или запрошены.</p>
                  <button className="secondary-button" onClick={onClose}>
                    Закрыть
                  </button>
                </>
              )}
            </div>
          </>
        )}
        {results && (
          <>
            <ul className="pack-list">
              {results.map((r) => (
                <li key={r.id}>
                  <span className={`status-pill ${r.ok ? "green" : "red"}`}>{r.ok ? "Отправлено" : "Ошибка"}</span>
                  <span>
                    <strong>{r.id}</strong>
                    <small>{r.text}</small>
                  </span>
                </li>
              ))}
            </ul>
            <div className="modal-actions">
              <p>Решения придут уведомлениями.</p>
              <button className="primary-button" onClick={onClose}>
                Готово
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
