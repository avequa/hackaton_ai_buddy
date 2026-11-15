import { useCallback, useEffect, useState } from "react";
import type { User } from "../api";
import { BRAND_NAME, BRAND_TAGLINE, BrandMark } from "../Brand";
import { Icon } from "../Icon";
import { ROLE_LABEL, TOKEN_KEY, api, errText, initials } from "../lib";

export function LoginScreen({ onLogin }: { onLogin: (u: User) => void }) {
  const [personas, setPersonas] = useState<User[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(() => {
    setError(null);
    setPersonas(null);
    api
      .personas()
      .then(setPersonas)
      .catch((e) => setError(errText(e)));
  }, []);

  useEffect(load, [load]);

  async function login(id: string) {
    setBusyId(id);
    setError(null);
    try {
      const { token, user } = await api.login(id);
      localStorage.setItem(TOKEN_KEY, token);
      onLogin(user);
    } catch (e) {
      setError(errText(e));
      setBusyId(null);
    }
  }

  return (
    <main className="login-screen">
      <div className="login-card">
        <div className="login-brand">
          <BrandMark />
          <div>
            <strong>{BRAND_NAME}</strong>
            <small>{BRAND_TAGLINE}</small>
          </div>
        </div>
        <h1>Войти как сотрудник</h1>
        <p>Тестовый вход без пароля — выберите, под кем зайти.</p>

        {error && (
          <div className="login-error">
            <span>{error}</span>
            {!personas && (
              <button onClick={load}>
                <Icon name="refresh" size={14} /> Повторить
              </button>
            )}
          </div>
        )}

        {!personas && !error && <div className="login-loading">Загружаем список…</div>}

        <div className="persona-grid">
          {personas?.map((p) => (
            <button key={p.id} className="persona-card" disabled={busyId !== null} onClick={() => login(p.id)}>
              <span className="avatar">{initials(p.name)}</span>
              <span>
                <strong>{p.name}</strong>
                <small>
                  {ROLE_LABEL[p.role]} · {p.position}, {p.team}
                </small>
              </span>
              {busyId === p.id ? <i className="spinner" /> : <Icon name="chevron" size={18} />}
            </button>
          ))}
        </div>
      </div>
    </main>
  );
}
