import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

/**
 * Минимальный роутер на History API (без зависимостей).
 * Учитывает base из Vite (import.meta.env.BASE_URL), поэтому работает и при деплое в подпапку.
 */

const basePath = (() => {
  try {
    return new URL(import.meta.env.BASE_URL, window.location.origin).pathname.replace(/\/+$/, "");
  } catch {
    return "";
  }
})();

export interface Location {
  /** Путь без base, всегда начинается с "/" и без хвостового слеша */
  path: string;
  /** Строка запроса вместе с "?" или "" */
  search: string;
}

function normalize(path: string): string {
  const p = path.replace(/\/{2,}/g, "/");
  return p.length > 1 ? p.replace(/\/+$/, "") : p || "/";
}

function readLocation(): Location {
  let path = window.location.pathname;
  if (basePath && (path === basePath || path.startsWith(`${basePath}/`))) path = path.slice(basePath.length);
  return { path: normalize(path || "/"), search: window.location.search };
}

export interface NavigateOptions {
  /** Заменить текущую запись в истории, а не добавлять новую */
  replace?: boolean;
}

interface RouterValue {
  location: Location;
  navigate: (to: string, options?: NavigateOptions) => void;
}

const RouterCtx = createContext<RouterValue | null>(null);

export function RouterProvider({ children }: { children: ReactNode }) {
  const [location, setLocation] = useState<Location>(readLocation);

  useEffect(() => {
    const onPop = () => setLocation(readLocation());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const navigate = useCallback((to: string, options?: NavigateOptions) => {
    const url = basePath + (to.startsWith("/") ? to : `/${to}`);
    const current = window.location.pathname + window.location.search;
    if (url === current) return;
    if (options?.replace) window.history.replaceState(null, "", url);
    else window.history.pushState(null, "", url);
    setLocation(readLocation());
    window.scrollTo(0, 0);
  }, []);

  const value = useMemo(() => ({ location, navigate }), [location, navigate]);
  return <RouterCtx.Provider value={value}>{children}</RouterCtx.Provider>;
}

export function useRouter(): RouterValue {
  const v = useContext(RouterCtx);
  if (!v) throw new Error("useRouter вне RouterProvider");
  return v;
}
