import { useState } from "react";
import { CONFIRM_LABEL, type Onboarding, type Step } from "../api";
import { Icon } from "../Icon";
import { useCtx } from "../context";
import { KIND_ICON, STATUS_TONE, api, errText } from "../lib";
import { StatusPill, Topbar } from "../parts";

/** Общий вид пути адаптации: для самого стажёра и для наставника, смотрящего на стажёра */
export function OnboardingView({
  data,
  canComplete,
  onComplete,
  onOpenRequest,
}: {
  data: Onboarding;
  canComplete: (s: Step) => boolean;
  onComplete: (s: Step) => Promise<void>;
  onOpenRequest?: (id: string) => void;
}) {
  const { nameOf, notify } = useCtx();
  const [busy, setBusy] = useState<string | null>(null);
  const pct = data.total > 0 ? Math.round((data.done / data.total) * 100) : 0;

  const weeks = new Map<number, Step[]>();
  for (const s of data.steps) weeks.set(s.week, [...(weeks.get(s.week) ?? []), s]);
  const orderedWeeks = [...weeks.entries()].sort((a, b) => a[0] - b[0]);

  async function complete(s: Step) {
    setBusy(s.id);
    try {
      await onComplete(s);
    } catch (e) {
      notify(errText(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="ob-wrap">
      <section className="ob-summary">
        <div>
          <p>Прогресс</p>
          <h2>
            {data.done} из {data.total} шагов
          </h2>
          <div className="progress-track">
            <i style={{ width: `${pct}%` }} />
          </div>
        </div>
        {data.next ? (
          <div className="ob-next">
            <small>Следующий шаг</small>
            <strong>{data.next.title}</strong>
            <span>Неделя {data.next.week}</span>
          </div>
        ) : (
          <div className="ob-next done">
            <small>Готово</small>
            <strong>Все шаги пройдены</strong>
          </div>
        )}
      </section>

      {orderedWeeks.map(([week, steps]) => (
        <section className="ob-week" key={week}>
          <h3>Неделя {week}</h3>
          {steps.map((s) => {
            const isNext = data.next?.id === s.id;
            return (
              <div key={s.id} className={`ob-step ${s.done ? "done" : ""} ${isNext ? "current" : ""}`}>
                <span className="ob-check">{s.done ? <Icon name="check" size={14} /> : isNext ? <Icon name="arrow" size={14} /> : null}</span>
                <div>
                  <strong>{s.title}</strong>
                  <small>
                    {s.done
                      ? `Отмечено${s.done_by ? `: ${nameOf(s.done_by)}` : ""}`
                      : (s.hint ?? CONFIRM_LABEL[s.confirm])}
                  </small>
                  {!s.done && s.hint && <small className="who">{CONFIRM_LABEL[s.confirm]}</small>}
                </div>
                {!s.done && canComplete(s) && (
                  <button className="primary-button" disabled={busy !== null} onClick={() => complete(s)}>
                    {busy === s.id ? "…" : "Готово"}
                  </button>
                )}
              </div>
            );
          })}
        </section>
      ))}

      {data.open_requests.length > 0 && (
        <section className="ob-week">
          <h3>Открытые заявки</h3>
          {data.open_requests.map((r) => (
            <button key={r.id} className="mini-request" onClick={() => onOpenRequest?.(r.id)} disabled={!onOpenRequest}>
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
        </section>
      )}
    </div>
  );
}

export function OnboardingScreen() {
  const { onboarding, onboardingError, refreshOnboarding, openRequest } = useCtx();
  return (
    <main className="content-area onboarding-screen">
      <Topbar title="Путь адаптации" subtitle="Шаги первых недель и кто их отмечает" />
      {!onboarding && onboardingError ? (
        <div className="login-error page-body">
          <span>{onboardingError}</span>
          <button onClick={() => void refreshOnboarding()}>
            <Icon name="refresh" size={14} /> Повторить
          </button>
        </div>
      ) : !onboarding ? (
        <div className="screen-loading">Загружаем путь…</div>
      ) : (
        <OnboardingView
          data={onboarding}
          canComplete={(s) => s.confirm === "self"}
          onComplete={async (s) => {
            await api.completeStep(s.id);
            await refreshOnboarding();
          }}
          onOpenRequest={openRequest}
        />
      )}
    </main>
  );
}
