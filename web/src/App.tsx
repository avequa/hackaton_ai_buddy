import { useCallback, useEffect, useMemo, useState } from "react";
import { hasPending, type Notification, type Onboarding, type Request as Req, type User } from "./api";
import { BRAND_NAME, BRAND_TAGLINE, BrandMark } from "./Brand";
import { Icon, type IconName } from "./Icon";
import { Ctx, useCtx as useCtx, type ModuleId, type Screen, type WorkspaceCtx } from "./context";
import {
  ROLE_LABEL,
  TOKEN_KEY,
  UNAUTH_EVENT,
  api,
  errText,
  initials,
  isOpen,
  requestsSignature,
} from "./lib";
import { useRouter } from "./router";
import { SCREEN_PATH, paths, safeNext, useRoute } from "./routes";
import { NotFound } from "./parts";
import { useChat } from "./useChat";
import { useFeed } from "./useFeed";
import { StarterPackDialog } from "./feed";
import { IncidentsScreen } from "./screens/Incidents";
import { AccessScreen } from "./screens/Access";
import { ApprovalsScreen } from "./screens/Approvals";
import { ChatScreen } from "./screens/Chat";
import { LoginScreen } from "./screens/Login";
import { MenteesScreen } from "./screens/Mentees";
import { ModulesScreen } from "./screens/Modules";
import { OnboardingScreen } from "./screens/Onboarding";
import { RequestsScreen } from "./screens/Requests";

/** Вход: если в localStorage есть токен - проверяем его через /api/me, иначе уводим на /login */
export default function App() {
  const { location, navigate } = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [checking, setChecking] = useState(() => !!localStorage.getItem(TOKEN_KEY));

  // Ручной выход: чистим токен и идём на чистый /login (без ?next)
  const logout = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    navigate(paths.login, { replace: true });
    setUser(null);
  }, [navigate]);

  // Токен протух (401): остаёмся на странице, /login запомнит её в ?next
  const onUnauthorized = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    setUser(null);
  }, []);

  useEffect(() => {
    if (!localStorage.getItem(TOKEN_KEY)) return;
    let alive = true;
    api
      .me()
      .then((u) => alive && setUser(u))
      .catch(() => {
        // 401 уже обработан onUnauthorized; при недоступном ядре тоже возвращаем на вход
        if (alive) localStorage.removeItem(TOKEN_KEY);
      })
      .finally(() => alive && setChecking(false));
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    window.addEventListener(UNAUTH_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTH_EVENT, onUnauthorized);
  }, [onUnauthorized]);

  // Не вошли -> /login?next=<куда шли>; вошли и открыли /login -> на главную
  const onLoginPage = location.path === paths.login;
  useEffect(() => {
    if (checking) return;
    if (!user && !onLoginPage) {
      const next = location.path + location.search;
      navigate(`${paths.login}?next=${encodeURIComponent(next)}`, { replace: true });
    } else if (user && onLoginPage) {
      navigate(safeNext(new URLSearchParams(location.search).get("next")) ?? paths.modules, { replace: true });
    }
  }, [checking, user, onLoginPage, location.path, location.search, navigate]);

  if (checking) return <div className="boot-screen">Загрузка…</div>;
  if (!user) return onLoginPage ? <LoginScreen onLogin={setUser} /> : null;
  if (onLoginPage) return null;
  return <Workspace key={user.id} user={user} onLogout={logout} />;
}

function Workspace({ user, onLogout }: { user: User; onLogout: () => void }) {
  const isIntern = user.role === "intern";
  const canIncidents = user.role === "sre" || user.role === "lead";
  const canMentor = user.role === "mentor" || user.role === "lead";
  const { navigate } = useRouter();
  const route = useRoute();

  // Модуль чата берём из URL (/chat/:module), а для кнопок «Чат» помним последний выбранный
  const [lastModule, setLastModule] = useState<ModuleId>("access");
  const routeModule = route.screen === "chat" ? route.moduleId : undefined;
  const activeModule = routeModule ?? lastModule;
  useEffect(() => {
    if (routeModule) setLastModule(routeModule);
  }, [routeModule]);

  const selectedRequestId = route.screen === "requests" ? (route.requestId ?? null) : null;
  const [toast, setToast] = useState<{ id: number; text: string; tone: "error" | "ok" } | null>(null);

  // ---- справочник имён ----
  const [names, setNames] = useState<Record<string, string>>({ [user.id]: user.name });
  useEffect(() => {
    api
      .personas()
      .then((list) => setNames((prev) => ({ ...prev, ...Object.fromEntries(list.map((p) => [p.id, p.name])) })))
      .catch(() => {});
  }, []);
  const nameOf = useCallback((id?: string) => (id ? (names[id] ?? id) : ""), [names]);

  // ---- заявки + опрос ----
  const [requests, setRequests] = useState<Req[]>([]);
  const [requestsLoaded, setRequestsLoaded] = useState(false);
  const refreshRequests = useCallback(async () => {
    try {
      setRequests(await api.requests());
    } catch {
      /* временная ошибка при опросе не должна мешать работе */
    } finally {
      setRequestsLoaded(true);
    }
  }, []);
  const upsertRequest = useCallback((r: Req) => {
    setRequests((prev) => (prev.some((x) => x.id === r.id) ? prev.map((x) => (x.id === r.id ? r : x)) : [r, ...prev]));
  }, []);

  useEffect(() => {
    void refreshRequests();
  }, [refreshRequests]);

  const pending = hasPending(requests);
  useEffect(() => {
    if (!pending) return;
    const t = setInterval(() => void refreshRequests(), 4000);
    return () => clearInterval(t);
  }, [pending, refreshRequests]);

  // ---- путь адаптации (стажёр) ----
  const [onboarding, setOnboarding] = useState<Onboarding | null>(null);
  const [onboardingError, setOnboardingError] = useState<string | null>(null);
  const refreshOnboarding = useCallback(async () => {
    if (!isIntern) return;
    try {
      setOnboarding(await api.onboarding());
      setOnboardingError(null);
    } catch (e) {
      // прошлое значение оставляем, но ошибку показываем - раньше экран молча висел на «Загружаем путь…»
      setOnboardingError(errText(e));
    }
  }, [isIntern]);
  const signature = requestsSignature(requests);
  // авто-шаги закрываются, когда доступ выдан - поэтому перечитываем при смене статусов
  useEffect(() => {
    void refreshOnboarding();
  }, [refreshOnboarding, signature]);

  // ---- очередь согласований (наставник/руководитель) ----
  const [approvals, setApprovals] = useState<Req[] | null>(null);
  const refreshApprovals = useCallback(async () => {
    if (!canMentor) return;
    try {
      setApprovals(await api.approvals());
    } catch {
      /* ignore */
    }
  }, [canMentor]);
  useEffect(() => {
    if (!canMentor) return;
    void refreshApprovals();
    const t = setInterval(() => void refreshApprovals(), 10_000);
    return () => clearInterval(t);
  }, [canMentor, refreshApprovals]);

  // ---- чат ----
  const afterTurn = useCallback(() => {
    void refreshRequests();
  }, [refreshRequests]);
  const chat = useChat(user.id, upsertRequest, afterTurn);

  // ---- уведомления ----
  const notify = useCallback((text: string, tone: "error" | "ok" = "error") => {
    setToast({ id: Date.now(), text, tone });
  }, []);
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 5000);
    return () => clearTimeout(t);
  }, [toast]);

  // ---- лента уведомлений и сбои ----
  const onNewNotes = useCallback(
    (notes: Notification[], initial: boolean) => {
      if (notes.some((n) => n.kind === "request")) void refreshRequests();
      if (notes.some((n) => n.kind === "approval")) void refreshApprovals();
      if (initial) return;
      const last = notes[notes.length - 1];
      const bad = last.kind === "incident" || last.kind === "security";
      setToast({ id: Date.now(), text: last.text ? `${last.title}: ${last.text}` : last.title, tone: bad ? "error" : "ok" });
    },
    [refreshRequests, refreshApprovals],
  );
  const feed = useFeed(onNewNotes);
  const [starterOpen, setStarterOpen] = useState(false);
  const openStarterPack = useCallback(() => setStarterOpen(true), []);

  // ---- навигация ----
  const go = useCallback(
    (target: Screen) => navigate(target === "chat" ? paths.chat(lastModule) : SCREEN_PATH[target]),
    [navigate, lastModule],
  );
  const openRequest = useCallback((id: string) => navigate(paths.request(id)), [navigate]);
  const selectModule = useCallback((id: ModuleId) => navigate(paths.chat(id)), [navigate]);
  const openNote = useCallback(
    (n: Notification) => {
      switch (n.kind) {
        case "request":
          if (n.ref) navigate(paths.request(n.ref));
          break;
        case "approval":
          navigate(paths.approvals);
          break;
        case "security":
        case "digest":
          if (n.ref) navigate(paths.mentee(n.ref));
          break;
        case "starter_pack":
          setStarterOpen(true);
          break;
        default:
          if (canIncidents) navigate(paths.incidents);
      }
    },
    [navigate, canIncidents],
  );

  // Переходы по «неполным» или недоступным ролью адресам: / -> /modules, /chat -> /chat/<модуль>
  const redirectTo: string | null = (() => {
    switch (route.screen) {
      case "root":
        return paths.modules;
      case "chat":
        return route.moduleId ? null : paths.chat(lastModule);
      case "onboarding":
        return isIntern ? null : paths.modules;
      case "mentees":
      case "approvals":
        return canMentor ? null : paths.modules;
      case "incidents":
        return canIncidents ? null : paths.modules;
      default:
        return null;
    }
  })();
  useEffect(() => {
    if (redirectTo) navigate(redirectTo, { replace: true });
  }, [redirectTo, navigate]);

  // Заголовок вкладки браузера
  const pageTitle: Partial<Record<typeof route.screen, string>> = {
    modules: "Модули",
    chat: "Чат с Buddy",
    requests: "Мои заявки",
    onboarding: "Путь адаптации",
    access: "Мои доступы",
    mentees: "Стажёры",
    approvals: "Согласования",
    incidents: "Сбои",
    notfound: "Страница не найдена",
  };
  const title = pageTitle[route.screen];
  useEffect(() => {
    document.title = title ? `${title} · ${BRAND_NAME}` : BRAND_NAME;
  }, [title]);

  const ctx: WorkspaceCtx = useMemo(
    () => ({
      user,
      nameOf,
      requests,
      requestsLoaded,
      upsertRequest,
      refreshRequests,
      onboarding,
      onboardingError,
      refreshOnboarding,
      refreshApprovals,
      approvalsCount: approvals?.length ?? 0,
      notify,
      go,
      openRequest,
      selectedRequestId,
      feed,
      openNote,
      openStarterPack,
    }),
    [user, nameOf, requests, requestsLoaded, upsertRequest, refreshRequests, onboarding, onboardingError, refreshOnboarding, refreshApprovals, approvals, notify, go, openRequest, selectedRequestId, feed, openNote, openStarterPack],
  );

  return (
    <Ctx.Provider value={ctx}>
      <div className="app-shell">
        <Sidebar
          screen={route.screen}
          go={go}
          onLogout={onLogout}
          activeCount={requests.filter(isOpen).length}
          approvalsCount={approvals?.length ?? 0}
          incidentsCount={feed.openIncidents.length}
        />
        {route.screen === "modules" && <ModulesScreen selectModule={selectModule} />}
        {route.screen === "chat" && route.moduleId && (
          <ChatScreen activeModule={activeModule} setActiveModule={selectModule} chat={chat} />
        )}
        {route.screen === "requests" && <RequestsScreen />}
        {route.screen === "onboarding" && isIntern && <OnboardingScreen />}
        {route.screen === "access" && <AccessScreen />}
        {route.screen === "mentees" && canMentor && <MenteesScreen />}
        {route.screen === "approvals" && canMentor && <ApprovalsScreen approvals={approvals} />}
        {route.screen === "incidents" && canIncidents && <IncidentsScreen />}
        {starterOpen && <StarterPackDialog onClose={() => setStarterOpen(false)} />}
        {route.screen === "notfound" && <NotFound />}
        {toast && (
          <div className={`toast ${toast.tone}`} role="status" key={toast.id} onClick={() => setToast(null)}>
            {toast.text}
          </div>
        )}
      </div>
    </Ctx.Provider>
  );
}

function Sidebar({
  screen,
  go,
  onLogout,
  activeCount,
  approvalsCount,
  incidentsCount,
}: {
  screen: string;
  go: (value: Screen) => void;
  onLogout: () => void;
  activeCount: number;
  approvalsCount: number;
  incidentsCount: number;
}) {
  const { user } = useCtx();
  const nav: { id: Screen; label: string; icon: IconName; badge?: number }[] = [
    { id: "modules", label: "Модули", icon: "grid" },
    { id: "chat", label: "Чат с Buddy", icon: "chat" },
    { id: "requests", label: "Мои заявки", icon: "file", badge: activeCount },
    { id: "access", label: "Мои доступы", icon: "key" },
  ];
  if (user.role === "intern") nav.push({ id: "onboarding", label: "Путь адаптации", icon: "route" });
  if (user.role === "mentor" || user.role === "lead") {
    nav.push({ id: "mentees", label: "Стажёры", icon: "users" });
    nav.push({ id: "approvals", label: "Согласования", icon: "check", badge: approvalsCount });
  }
  if (user.role === "sre" || user.role === "lead") nav.push({ id: "incidents", label: "Сбои", icon: "alert", badge: incidentsCount });

  return (
    <aside className="sidebar">
      <button className="brand" onClick={() => go("modules")} aria-label="На главную">
        <BrandMark />
        <span>
          <strong>{BRAND_NAME}</strong>
          <small>{BRAND_TAGLINE}</small>
        </span>
      </button>
      <nav className="main-nav">
        <p>Рабочее пространство</p>
        {nav.map((item) => (
          <button key={item.id} className={screen === item.id ? "active" : ""} onClick={() => go(item.id)}>
            <Icon name={item.icon} size={19} />
            <span>{item.label}</span>
            {!!item.badge && <b>{item.badge}</b>}
          </button>
        ))}
      </nav>
      <div className="sidebar-bottom">
        <div className="security-note">
          <Icon name="shield" size={17} />
          <span>
            <strong>Защищённый контур</strong>
            <small>Данные не покидают компанию</small>
          </span>
        </div>
        <button className="profile" onClick={onLogout} title="Выйти">
          <span className="avatar">{initials(user.name)}</span>
          <span>
            <strong>{user.name}</strong>
            <small>
              {ROLE_LABEL[user.role]} · {user.position}
            </small>
          </span>
          <Icon name="logout" size={18} />
        </button>
      </div>
    </aside>
  );
}
