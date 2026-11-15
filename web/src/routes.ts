import type { ModuleId, Screen } from "./context";
import { useRouter } from "./router";

/** Один путь на каждую страницу. Всё, что связано с URL, лежит здесь. */
export const paths = {
  login: "/login",
  modules: "/modules",
  chat: (module?: ModuleId) => (module ? `/chat/${module}` : "/chat"),
  requests: "/requests",
  request: (id: string) => `/requests/${encodeURIComponent(id)}`,
  access: "/access",
  onboarding: "/onboarding",
  mentees: "/mentees",
  mentee: (userId: string) => `/mentees/${encodeURIComponent(userId)}`,
  approvals: "/approvals",
  incidents: "/incidents",
} as const;

export const MODULE_IDS: readonly ModuleId[] = ["access", "support", "equipment"];
const isModuleId = (v: string): v is ModuleId => (MODULE_IDS as readonly string[]).includes(v);

/** Куда ведёт «экран» без параметров (для навигации по Screen) */
export const SCREEN_PATH: Record<Exclude<Screen, "chat">, string> = {
  modules: paths.modules,
  requests: paths.requests,
  onboarding: paths.onboarding,
  access: paths.access,
  mentees: paths.mentees,
  approvals: paths.approvals,
  incidents: paths.incidents,
};

export type Route =
  | { screen: "login" }
  | { screen: "root" }
  | { screen: "modules" }
  | { screen: "chat"; moduleId?: ModuleId }
  | { screen: "requests"; requestId?: string }
  | { screen: "onboarding" }
  | { screen: "access" }
  | { screen: "mentees"; menteeId?: string }
  | { screen: "approvals" }
  | { screen: "incidents" }
  | { screen: "notfound" };

const decode = (s: string) => {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
};

export function matchRoute(path: string): Route {
  const [first = "", second, ...rest] = path.split("/").filter(Boolean);
  if (rest.length > 0) return { screen: "notfound" };

  switch (first) {
    case "":
      return second === undefined ? { screen: "root" } : { screen: "notfound" };
    case "login":
      return second === undefined ? { screen: "login" } : { screen: "notfound" };
    case "modules":
      return second === undefined ? { screen: "modules" } : { screen: "notfound" };
    case "onboarding":
      return second === undefined ? { screen: "onboarding" } : { screen: "notfound" };
    case "access":
      return second === undefined ? { screen: "access" } : { screen: "notfound" };
    case "approvals":
      return second === undefined ? { screen: "approvals" } : { screen: "notfound" };
    case "incidents":
      return second === undefined ? { screen: "incidents" } : { screen: "notfound" };
    case "chat":
      if (second === undefined) return { screen: "chat" };
      return isModuleId(second) ? { screen: "chat", moduleId: second } : { screen: "notfound" };
    case "requests":
      return { screen: "requests", requestId: second === undefined ? undefined : decode(second) };
    case "mentees":
      return { screen: "mentees", menteeId: second === undefined ? undefined : decode(second) };
    default:
      return { screen: "notfound" };
  }
}

export function useRoute(): Route {
  const { location } = useRouter();
  return matchRoute(location.path);
}

/** Куда вернуть после входа: только внутренний путь, чтобы нельзя было увести на чужой сайт */
export function safeNext(raw: string | null): string | null {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return null;
  return raw.startsWith(paths.login) ? null : raw;
}
