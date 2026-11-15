import { useMemo, useState } from "react";
import { STATUS_LABEL, type Kind, type Request as Req } from "../api";
import { Icon } from "../Icon";
import { useCtx } from "../context";
import {
  KIND_ICON,
  KIND_LABEL,
  STATUS_TONE,
  api,
  canCancel,
  errText,
  fmtDate,
  isFinal,
  isOpen,
} from "../lib";
import { EmptyState, Notes, RequestBody, RequestRow, StatusPill, Topbar } from "../parts";

type Tab = "all" | "active" | "done";

export function RequestsScreen() {
  const { requests, requestsLoaded, refreshRequests, selectedRequestId, openRequest } = useCtx();
  const [tab, setTab] = useState<Tab>("all");
  const [kind, setKind] = useState<Kind | "">("");
  const [query, setQuery] = useState("");
  const [refreshing, setRefreshing] = useState(false);

  const activeCount = requests.filter(isOpen).length;

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return requests.filter((r) => {
      if (tab === "active" && !isOpen(r)) return false;
      if (tab === "done" && isOpen(r)) return false;
      if (kind && r.kind !== kind) return false;
      return !q || `${r.id} ${r.title}`.toLowerCase().includes(q);
    });
  }, [requests, tab, kind, query]);

  const selected = requests.find((r) => r.id === selectedRequestId) ?? filtered[0] ?? null;

  async function refresh() {
    setRefreshing(true);
    await refreshRequests();
    setRefreshing(false);
  }

  const tabs: [Tab, string][] = [
    ["all", "Все"],
    ["active", "Активные"],
    ["done", "Завершённые"],
  ];

  return (
    <main className="content-area requests-screen">
      <Topbar title="Мои заявки" subtitle="Все обращения и их актуальный статус" />
      <div className="requests-tools">
        <div className="filter-tabs">
          {tabs.map(([id, label]) => (
            <button key={id} className={tab === id ? "active" : ""} onClick={() => setTab(id)}>
              {label}
              {id === "active" && activeCount > 0 && <span>{activeCount}</span>}
            </button>
          ))}
        </div>
        <div className="tool-actions">
          <label className="search-field">
            <Icon name="search" size={17} />
            <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Поиск по заявкам" />
          </label>
          <label className="filter-button">
            <Icon name="filter" size={17} />
            <select value={kind} onChange={(e) => setKind(e.target.value as Kind | "")}>
              <option value="">Все виды</option>
              {(Object.keys(KIND_LABEL) as Kind[]).map((k) => (
                <option key={k} value={k}>
                  {KIND_LABEL[k]}
                </option>
              ))}
            </select>
          </label>
        </div>
      </div>
      <div className="requests-layout">
        <section className="request-list">
          <div className="list-caption">
            <span>{filtered.length} заявок</span>
            <button onClick={refresh} disabled={refreshing}>
              <Icon name="refresh" size={14} /> {refreshing ? "Обновляем…" : "Обновить"}
            </button>
          </div>
          {filtered.map((r) => (
            <RequestRow key={r.id} req={r} selected={selected?.id === r.id} onClick={() => openRequest(r.id)} />
          ))}
          {!filtered.length && (
            <EmptyState
              title={requestsLoaded ? "Ничего не найдено" : "Загружаем заявки…"}
              text={requestsLoaded ? (requests.length ? "Попробуйте изменить запрос или фильтр." : "Заявок пока нет — опишите задачу в чате.") : undefined}
            />
          )}
        </section>
        {selected ? <Detail key={selected.id} req={selected} /> : <aside className="request-detail" />}
      </div>
    </main>
  );
}

function Detail({ req }: { req: Req }) {
  const { nameOf, go, upsertRequest, refreshRequests, notify } = useCtx();
  const [busy, setBusy] = useState<"confirm" | "cancel" | null>(null);
  const tone = STATUS_TONE[req.status];

  async function act(kind: "confirm" | "cancel") {
    setBusy(kind);
    try {
      upsertRequest(kind === "confirm" ? await api.confirm(req.id) : await api.cancel(req.id));
    } catch (e) {
      notify(errText(e));
      void refreshRequests();
    } finally {
      setBusy(null);
    }
  }

  return (
    <aside className="request-detail">
      <div className="detail-header">
        <div>
          <span>{req.id}</span>
          <h2>{req.title}</h2>
        </div>
      </div>
      <div className="detail-status">
        <span className={`request-icon ${tone}`}>
          <Icon name={KIND_ICON[req.kind]} size={20} />
        </span>
        <div>
          <small>ТЕКУЩИЙ СТАТУС</small>
          <strong>{STATUS_LABEL[req.status]}</strong>
        </div>
        <StatusPill status={req.status} />
      </div>

      <div className="detail-section">
        <h3>Детали заявки</h3>
        <dl>
          <div>
            <dt>Тип</dt>
            <dd>{KIND_LABEL[req.kind]}</dd>
          </div>
          <div>
            <dt>Создана</dt>
            <dd>{fmtDate(req.created_at)}</dd>
          </div>
          <div>
            <dt>Обновлена</dt>
            <dd>{fmtDate(req.updated_at)}</dd>
          </div>
          {req.approver_name && (
            <div>
              <dt>Согласующий</dt>
              <dd>{req.approver_name}</dd>
            </div>
          )}
          <div>
            <dt>Заявитель</dt>
            <dd>{nameOf(req.user)}</dd>
          </div>
        </dl>
        <div className="detail-body">
          <RequestBody req={req} />
          <Notes req={req} />
        </div>
      </div>

      <div className="detail-section">
        <h3>История</h3>
        <div className="timeline">
          {req.history.map((h, i) => (
            <div className="active" key={`${h.at}-${i}`}>
              <span>
                <Icon name="check" size={12} />
              </span>
              <div>
                <strong>{STATUS_LABEL[h.status as keyof typeof STATUS_LABEL] ?? h.status}</strong>
                <small>
                  {fmtDate(h.at)} · {nameOf(h.by)}
                </small>
                {h.note && <small className="note">{h.note}</small>}
              </div>
            </div>
          ))}
          {!isFinal(req.status) && req.status !== "draft" && (
            <div>
              <span>
                <Icon name="clock" size={12} />
              </span>
              <div>
                <strong>Ожидает решения</strong>
                <small>Статус обновится автоматически</small>
              </div>
            </div>
          )}
        </div>
      </div>

      <div className="detail-actions">
        {req.status === "draft" && (
          <button className="primary-button" disabled={busy !== null} onClick={() => act("confirm")}>
            <Icon name="check" size={16} /> {busy === "confirm" ? "Отправляем…" : "Подтвердить"}
          </button>
        )}
        {canCancel(req) && (
          <button className="secondary-button" disabled={busy !== null} onClick={() => act("cancel")}>
            {busy === "cancel" ? "Отменяем…" : "Отменить заявку"}
          </button>
        )}
        <button className="link-button" onClick={() => go("chat")}>
          <Icon name="chat" size={15} /> Задать вопрос
        </button>
      </div>
    </aside>
  );
}
