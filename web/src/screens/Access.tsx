import { useEffect, useState } from "react";
import type { Grant, Request as Req } from "../api";
import { useCtx } from "../context";
import { LEVEL_LABEL, api, errText, requestsSignature } from "../lib";
import { EmptyState, RequestRow, Topbar } from "../parts";

export function AccessScreen() {
  const { requests, openRequest } = useCtx();
  const [grants, setGrants] = useState<Grant[] | null>(null);
  const [open, setOpen] = useState<Req[]>([]);
  const [error, setError] = useState<string | null>(null);
  const sig = requestsSignature(requests);

  // Перезагружаем при смене статусов заявок: выданный доступ появится в списке сам
  useEffect(() => {
    let alive = true;
    api
      .access()
      .then((r) => {
        if (!alive) return;
        setGrants(r.grants);
        setOpen(r.open_requests);
        setError(null);
      })
      .catch((e) => alive && setError(errText(e)));
    return () => {
      alive = false;
    };
  }, [sig]);

  return (
    <main className="content-area access-screen">
      <Topbar title="Мои доступы" subtitle="Что у вас уже есть и что ждёт согласования" />
      <div className="page-body">
        {error && <div className="login-error">{error}</div>}
        <section className="page-card">
          <h3>Выданные доступы</h3>
          {grants === null && !error && <div className="screen-loading">Загружаем…</div>}
          {grants?.length === 0 && (
            <EmptyState icon="check" title="Пока ничего нет" text="Опишите задачу в чате — Buddy подготовит заявку на нужные доступы." />
          )}
          {grants?.map((g) => {
            const svc = g.service.toLowerCase();
            return (
              <div className="access-row bordered" key={`${g.service}/${g.project}`}>
                <span className={`service-logo ${svc.includes("git") ? "git" : svc.includes("graf") ? "graf" : ""}`}>
                  {g.service.slice(0, 2).toUpperCase()}
                </span>
                <div>
                  <strong>
                    {g.service} · {g.project}
                  </strong>
                  <small>Уровень: {LEVEL_LABEL[g.level]}</small>
                </div>
                <span className="status-pill green">Активен</span>
              </div>
            );
          })}
        </section>

        {open.length > 0 && (
          <section className="page-card">
            <h3>Заявки на доступ в процессе</h3>
            {open.map((r) => (
              <RequestRow key={r.id} req={r} onClick={() => openRequest(r.id)} />
            ))}
          </section>
        )}
      </div>
    </main>
  );
}
