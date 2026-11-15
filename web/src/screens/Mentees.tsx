import { useCallback, useEffect, useState } from "react";
import type { Digest, Mentee, Onboarding, SecurityEvent, Step } from "../api";
import { Icon } from "../Icon";
import { useCtx } from "../context";
import { SECURITY_KIND_LABEL, api, errText, fmtAgo, initials } from "../lib";
import { EmptyState, Topbar } from "../parts";
import { useRouter } from "../router";
import { paths, useRoute } from "../routes";
import { OnboardingView } from "./Onboarding";

export function MenteesScreen() {
  const { user, openRequest, refreshApprovals } = useCtx();
  const [mentees, setMentees] = useState<Mentee[] | null>(null);
  const [events, setEvents] = useState<SecurityEvent[]>([]);
  const [error, setError] = useState<string | null>(null);
  const { navigate } = useRouter();
  const route = useRoute();
  // выбранный стажёр живёт в адресе: /mentees/:id
  const openId = route.screen === "mentees" ? (route.menteeId ?? null) : null;

  const load = useCallback(async () => {
    try {
      setMentees(await api.mentees());
      setEvents(await api.securityEvents().catch(() => []));
      setError(null);
    } catch (e) {
      setError(errText(e));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const current = mentees?.find((m) => m.user.id === openId) ?? null;

  return (
    <main className="content-area mentees-screen">
      <Topbar
        title={current ? current.user.name : "Мои стажёры"}
        subtitle={current ? `${current.user.position}, ${current.user.team}` : "Прогресс адаптации и ожидающие решения"}
        right={
          current && (
            <button className="secondary-button" onClick={() => navigate(paths.mentees)}>
              ← Все стажёры
            </button>
          )
        }
      />
      {error && <div className="login-error page-body">{error}</div>}
      {!current && (
        <div className="page-body">
          {mentees === null && !error && <div className="screen-loading">Загружаем…</div>}
          {mentees?.length === 0 && <EmptyState icon="users" title="Пока нет стажёров" />}
          <div className="mentee-grid">
            {mentees?.map((m) => {
              const pct = m.total > 0 ? Math.round((m.done / m.total) * 100) : 0;
              return (
                <button key={m.user.id} className="mentee-card" onClick={() => navigate(paths.mentee(m.user.id))}>
                  <div className="mentee-top">
                    <span className="avatar">{initials(m.user.name)}</span>
                    <span>
                      <strong>{m.user.name}</strong>
                      <small>
                        {m.user.position} · {m.user.team}
                      </small>
                    </span>
                    {m.pending_approvals > 0 && <b className="badge-count">{m.pending_approvals}</b>}
                  </div>
                  <div className="progress-track">
                    <i style={{ width: `${pct}%` }} />
                  </div>
                  <div className="mentee-meta">
                    <span>
                      {m.done} из {m.total} шагов
                    </span>
                    {m.pending_approvals > 0 && <span className="pending">Ждут решения: {m.pending_approvals}</span>}
                  </div>
                  <p>{m.next ? `Дальше: ${m.next.title}` : "Все шаги пройдены"}</p>
                </button>
              );
            })}
          </div>
          {events.length > 0 && <SecurityList events={events} onOpen={(id) => navigate(paths.mentee(id))} />}
        </div>
      )}
      {current && (
        <MenteeDetail
          key={current.user.id}
          userId={current.user.id}
          canLead={user.role === "lead"}
          onChanged={() => {
            void load();
            void refreshApprovals();
          }}
          onOpenRequest={openRequest}
        />
      )}
    </main>
  );
}

function MenteeDetail({
  userId,
  canLead,
  onChanged,
  onOpenRequest,
}: {
  userId: string;
  canLead: boolean;
  onChanged: () => void;
  onOpenRequest: (id: string) => void;
}) {
  const [data, setData] = useState<Onboarding | null>(null);
  const [digest, setDigest] = useState<Digest | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setData(await api.menteeOnboarding(userId));
      setDigest(await api.digest(userId).catch(() => null));
      setError(null);
    } catch (e) {
      setError(errText(e));
    }
  }, [userId]);

  useEffect(() => {
    void load();
  }, [load]);

  if (error) return <div className="login-error page-body">{error}</div>;
  if (!data) return <div className="screen-loading">Загружаем путь…</div>;

  return (
    <>
      {digest && <DigestCard digest={digest} />}
      <OnboardingView
      data={data}
      canComplete={(s: Step) => s.confirm === "mentor" || (canLead && s.confirm === "lead")}
      onComplete={async (s) => {
        await api.completeMenteeStep(userId, s.id);
        await load();
        onChanged();
      }}
      onOpenRequest={onOpenRequest}
      />
    </>
  );
}

/** Сводка к встрече 1:1: готовые темы для разговора и с чем стажер приходил к помощнику */
function DigestCard({ digest }: { digest: Digest }) {
  return (
    <section className="page-card digest-card">
      <div className="digest-head">
        <span className="draft-icon">
          <Icon name="users" size={19} />
        </span>
        <div>
          <strong>Сводка к встрече 1:1</strong>
          <small>
            Неделя {digest.week} · сделано {digest.done} из {digest.total} шагов
            {digest.next ? ` · дальше: ${digest.next.title}` : ""}
          </small>
        </div>
      </div>
      {digest.talking_points.length > 0 ? (
        <ol className="talking-points">
          {digest.talking_points.map((t) => (
            <li key={t}>{t}</li>
          ))}
        </ol>
      ) : (
        <p className="panel-empty">Всё идёт по плану — особых тем для разговора нет.</p>
      )}
      {(digest.topics.length > 0 || digest.unanswered_in_kb > 0) && (
        <div className="digest-topics">
          <small>С чем приходил к помощнику (без текста переписки):</small>
          <div className="module-tags">
            {digest.topics.map((t) => (
              <span key={t.topic}>
                {t.topic} × {t.count}
              </span>
            ))}
            {digest.unanswered_in_kb > 0 && <span className="warn">не нашлось в базе знаний: {digest.unanswered_in_kb}</span>}
          </div>
        </div>
      )}
      {digest.security_events.length > 0 && <SecurityList events={digest.security_events} compact />}
    </section>
  );
}

function SecurityList({ events, onOpen, compact = false }: { events: SecurityEvent[]; onOpen?: (userId: string) => void; compact?: boolean }) {
  return (
    <section className={compact ? "security-list compact" : "page-card security-list"}>
      <h3>
        <Icon name="shield" size={16} /> Попытки выйти за права
      </h3>
      {events.slice(0, 10).map((ev) => (
        <button key={ev.id} className="security-row" onClick={() => onOpen?.(ev.user)} disabled={!onOpen}>
          <span className="status-pill red">{SECURITY_KIND_LABEL[ev.kind] ?? ev.kind}</span>
          <span>
            <strong>{ev.name}</strong>
            <small>{ev.detail}</small>
          </span>
          <small>{fmtAgo(ev.at)}</small>
        </button>
      ))}
    </section>
  );
}

