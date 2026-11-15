import { useState } from "react";
import type { Request as Req } from "../api";
import { Icon } from "../Icon";
import { useCtx } from "../context";
import { KIND_ICON, KIND_LABEL, api, errText, fmtDate } from "../lib";
import { EmptyState, Notes, RequestBody, StatusPill, Topbar } from "../parts";

export function ApprovalsScreen({ approvals }: { approvals: Req[] | null }) {
  return (
    <main className="content-area approvals-screen">
      <Topbar title="Согласования" subtitle="Заявки стажёров не по роли — нужно ваше решение" />
      <div className="page-body">
        {approvals === null && <div className="screen-loading">Загружаем…</div>}
        {approvals?.length === 0 && (
          <EmptyState icon="check" title="Очередь пуста" text="Новые заявки появятся здесь, как только стажёры их подтвердят." />
        )}
        {approvals?.map((r) => (
          <ApprovalCard key={r.id} req={r} />
        ))}
      </div>
    </main>
  );
}

function ApprovalCard({ req }: { req: Req }) {
  const { nameOf, refreshApprovals, notify } = useCtx();
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState<"approve" | "reject" | null>(null);

  async function decide(decision: "approve" | "reject") {
    setBusy(decision);
    try {
      await api.decide(req.id, decision, comment.trim() || undefined);
      notify(decision === "approve" ? `Заявка ${req.id} одобрена` : `Заявка ${req.id} отклонена`, "ok");
      await refreshApprovals();
    } catch (e) {
      notify(errText(e));
      setBusy(null);
      void refreshApprovals();
    }
  }

  return (
    <article className="approval-card draft-card">
      <div className="draft-head">
        <div>
          <span className="draft-icon">
            <Icon name={KIND_ICON[req.kind]} size={19} />
          </span>
          <span>
            <small>
              {req.id} · {KIND_LABEL[req.kind].toUpperCase()}
            </small>
            <strong>{req.title}</strong>
          </span>
        </div>
        <StatusPill status={req.status} />
      </div>
      <p className="approval-meta">
        Заявитель: <b>{nameOf(req.user)}</b> · {fmtDate(req.created_at)}
      </p>
      <RequestBody req={req} />
      <Notes req={req} />
      <div className="approval-comment">
        <input
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          placeholder="Комментарий (при отказе стажёр увидит его как причину)"
          disabled={busy !== null}
        />
      </div>
      <div className="draft-actions">
        <button className="primary-button" disabled={busy !== null} onClick={() => decide("approve")}>
          <Icon name="check" size={17} /> {busy === "approve" ? "Одобряем…" : "Одобрить"}
        </button>
        <button className="secondary-button danger" disabled={busy !== null} onClick={() => decide("reject")}>
          {busy === "reject" ? "Отклоняем…" : "Отклонить"}
        </button>
      </div>
    </article>
  );
}
