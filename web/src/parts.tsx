import type { ReactNode } from "react";
import { STATUS_LABEL, type Request as Req, type Status } from "./api";
import { Icon } from "./Icon";
import { useCtx } from "./context";
import { Bell, IncidentBanner } from "./feed";
import {
  EQUIPMENT_ACTION_LABEL,
  KIND_ICON,
  KIND_LABEL,
  LEVEL_LABEL,
  ROLE_LABEL,
  STATUS_TONE,
  fmtDate,
} from "./lib";

export function StatusPill({ status }: { status: Status }) {
  return <span className={`status-pill ${STATUS_TONE[status]}`}>{STATUS_LABEL[status]}</span>;
}

export function Topbar({ title, subtitle, right }: { title: string; subtitle?: string; right?: ReactNode }) {
  const { user } = useCtx();
  return (
    <>
      <header className="topbar">
        <div>
          <h1>{title}</h1>
          {subtitle && <p>{subtitle}</p>}
        </div>
        <div className="top-actions">
          {right}
          <Bell />
          <div className="online">
            <span /> {ROLE_LABEL[user.role]}
          </div>
        </div>
      </header>
      <IncidentBanner />
    </>
  );
}

export function EmptyState({ icon = "search", title, text }: { icon?: "search" | "file" | "users" | "check"; title: string; text?: string }) {
  return (
    <div className="empty-list">
      <Icon name={icon} size={26} />
      <strong>{title}</strong>
      {text && <p>{text}</p>}
    </div>
  );
}

/** Что именно запрошено: зависит от вида заявки */
export function RequestBody({ req }: { req: Req }) {
  const logo = (text: string, extra = "") => (
    <span className={`service-logo ${extra}`}>{text.slice(0, 2).toUpperCase()}</span>
  );

  if (req.access) {
    const a = req.access;
    const svc = a.service.toLowerCase();
    return (
      <>
        <div className="access-row">
          {logo(a.service, svc.includes("git") ? "git" : svc.includes("graf") ? "graf" : "")}
          <div>
            <strong>
              {a.service} · {a.project}
            </strong>
            <small>Уровень: {LEVEL_LABEL[a.level]}</small>
          </div>
          {req.approver_name && <span className="approval">{req.approver_name}</span>}
        </div>
        {a.justification && <Reason title="Обоснование" text={a.justification} />}
      </>
    );
  }

  if (req.ticket) {
    const t = req.ticket;
    return (
      <>
        <div className="access-row">
          {logo("HD")}
          <div>
            <strong>{t.summary}</strong>
            <small>Сервис: {t.service}</small>
          </div>
        </div>
        {t.details && <Reason title="Детали" text={t.details} />}
        {t.steps_tried && t.steps_tried.length > 0 && (
          <Reason title="Уже пробовали" text={t.steps_tried.join("; ")} />
        )}
      </>
    );
  }

  if (req.equipment) {
    const e = req.equipment;
    return (
      <>
        <div className="access-row">
          {logo("EQ")}
          <div>
            <strong>
              {e.item} · {e.model}
            </strong>
            <small>{EQUIPMENT_ACTION_LABEL[e.action] ?? e.action}</small>
          </div>
        </div>
        {e.reason && <Reason title="Причина" text={e.reason} />}
      </>
    );
  }

  return null;
}

function Reason({ title, text }: { title: string; text: string }) {
  return (
    <div className="reason">
      <Icon name="spark" size={15} />
      <span>
        <strong>{title}</strong>
        {text}
      </span>
    </div>
  );
}

/** Заявка не по роли + итог/причина отказа */
export function Notes({ req, compact = false }: { req: Req; compact?: boolean }) {
  const needsMentor = req.policy.verdict === "needs_mentor";
  const showPolicy = needsMentor && (req.status === "draft" || req.status === "pending_mentor" || !compact);
  const tone = req.status === "rejected" ? "red" : "green";
  return (
    <>
      {showPolicy && (
        <div className="policy-note">
          <strong>Заявка не по вашей роли</strong>
          {req.policy.reasons && req.policy.reasons.length > 0 && (
            <ul>
              {req.policy.reasons.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          )}
          <p>
            {req.status === "draft"
              ? "Рекомендуем обсудить с наставником. После подтверждения заявка уйдёт к нему на решение."
              : "Заявка ждёт решения наставника."}
          </p>
        </div>
      )}
      {req.resolution && (
        <div className={`resolution-note ${tone}`}>
          <small>{req.status === "rejected" ? "Причина отказа" : "Итог"}</small>
          <strong>{req.resolution}</strong>
        </div>
      )}
    </>
  );
}

export function RequestRow({ req, selected, onClick }: { req: Req; selected?: boolean; onClick: () => void }) {
  const tone = STATUS_TONE[req.status];
  return (
    <button className={`request-row ${selected ? "selected" : ""}`} onClick={onClick}>
      <span className={`request-icon ${tone}`}>
        <Icon name={KIND_ICON[req.kind]} size={20} />
      </span>
      <span className="request-info">
        <span>
          <b>{req.id}</b>
          <small>{KIND_LABEL[req.kind]}</small>
        </span>
        <strong>{req.title}</strong>
        <small>{fmtDate(req.updated_at)}</small>
      </span>
      <StatusPill status={req.status} />
      <Icon name="chevron" size={18} className="row-chevron" />
    </button>
  );
}

export function NotFound() {
  const { go } = useCtx();
  return (
    <main className="content-area">
      <Topbar title="Страница не найдена" subtitle="Такого адреса в приложении нет" />
      <div className="page-body">
        <EmptyState title="Ошибка 404" text="Проверьте адрес или вернитесь на главную." />
        <button className="primary-button" onClick={() => go("modules")}>
          На главную
        </button>
      </div>
    </main>
  );
}
