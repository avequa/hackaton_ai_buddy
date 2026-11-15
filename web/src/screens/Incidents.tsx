import { useState, type FormEvent } from "react";
import { RADAR, type Incident } from "../api";
import { Icon } from "../Icon";
import { useCtx } from "../context";
import { SERVICE_LABEL, api, errText, fmtAgo, fmtDate, serviceLabel } from "../lib";
import { EmptyState, Topbar } from "../parts";

/** Сбои: дежурный SRE (и руководитель) объявляет и закрывает; радар открывает и закрывает сам */
export function IncidentsScreen() {
  const { feed, notify, nameOf } = useCtx();
  const [service, setService] = useState("vpn");
  const [title, setTitle] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState<string | null>(null);

  const open = feed.incidents.filter((i) => !i.resolved_at);
  const closed = feed.incidents.filter((i) => i.resolved_at).reverse().slice(0, 10);

  async function declare(e: FormEvent) {
    e.preventDefault();
    if (!title.trim()) return;
    setBusy("new");
    try {
      const inc = await api.openIncident({ service, title: title.trim(), message: message.trim() || undefined });
      notify(`${inc.id} объявлен, все получили уведомление`, "ok");
      setTitle("");
      setMessage("");
      await feed.refreshIncidents();
    } catch (err) {
      notify(errText(err));
    } finally {
      setBusy(null);
    }
  }

  async function resolve(inc: Incident) {
    setBusy(inc.id);
    try {
      await api.resolveIncident(inc.id);
      notify(`${inc.id} закрыт, всем ушло «восстановлено»`, "ok");
      await feed.refreshIncidents();
    } catch (err) {
      notify(errText(err));
    } finally {
      setBusy(null);
    }
  }

  const by = (inc: Incident) => (inc.by === RADAR ? "incident-radar, по логам NGINX" : nameOf(inc.by) || "дежурный");

  return (
    <main className="content-area incidents-screen">
      <Topbar title="Сбои" subtitle="Объявленный сбой сразу видят все: помощник не заводит тикеты-дубликаты" />
      <div className="page-body">
        <form className="page-card incident-form" onSubmit={declare}>
          <h3>Объявить сбой</h3>
          <div className="form-row">
            <label>
              Сервис
              <select value={service} onChange={(e) => setService(e.target.value)}>
                {Object.entries(SERVICE_LABEL).map(([id, label]) => (
                  <option key={id} value={id}>
                    {label}
                  </option>
                ))}
              </select>
            </label>
            <label className="grow">
              Что случилось
              <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Недоступен шлюз VPN в Москве" maxLength={200} />
            </label>
          </div>
          <label>
            Что делать сотрудникам (необязательно)
            <input value={message} onChange={(e) => setMessage(e.target.value)} placeholder="Работайте через резервный шлюз vpn2" maxLength={500} />
          </label>
          <button className="primary-button" disabled={busy !== null || !title.trim()}>
            <Icon name="alert" size={16} /> {busy === "new" ? "Объявляем…" : "Объявить и уведомить всех"}
          </button>
        </form>

        <section className="page-card">
          <h3>Открытые</h3>
          {open.length === 0 && <EmptyState icon="check" title="Сбоев нет" text="Радар проверяет логи VPN и GitLab каждые 10 секунд." />}
          {open.map((inc) => (
            <div className="incident-row open" key={inc.id}>
              <span className="incident-icon">
                <Icon name="alert" size={17} />
              </span>
              <div>
                <strong>
                  {serviceLabel(inc.service)}: {inc.title}
                </strong>
                <small>
                  {inc.id} · {by(inc)} · {fmtAgo(inc.since)}
                </small>
                {inc.message && <p>{inc.message}</p>}
              </div>
              <button className="secondary-button" disabled={busy !== null} onClick={() => resolve(inc)}>
                {busy === inc.id ? "Закрываем…" : "Закрыть"}
              </button>
            </div>
          ))}
        </section>

        {closed.length > 0 && (
          <section className="page-card">
            <h3>Недавно закрытые</h3>
            {closed.map((inc) => (
              <div className="incident-row" key={inc.id}>
                <span className="incident-icon done">
                  <Icon name="check" size={17} />
                </span>
                <div>
                  <strong>
                    {serviceLabel(inc.service)}: {inc.title}
                  </strong>
                  <small>
                    {inc.id} · {by(inc)} · {fmtDate(inc.since)} — {inc.resolved_at ? fmtDate(inc.resolved_at) : ""}
                  </small>
                </div>
              </div>
            ))}
          </section>
        )}
      </div>
    </main>
  );
}
