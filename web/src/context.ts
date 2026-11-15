import { createContext, useContext } from "react";
import type { Notification, Onboarding, Request as Req, User } from "./api";
import type { Feed } from "./useFeed";

export type Screen =
  | "modules"
  | "chat"
  | "requests"
  | "onboarding"
  | "access"
  | "mentees"
  | "approvals"
  | "incidents";

export type ModuleId = "access" | "support" | "equipment";

export interface WorkspaceCtx {
  user: User;
  /** id пользователя -> имя (из /api/personas), если неизвестен - сам id */
  nameOf: (id?: string) => string;
  requests: Req[];
  requestsLoaded: boolean;
  upsertRequest: (r: Req) => void;
  refreshRequests: () => Promise<void>;
  /** Путь адаптации (только у стажёра, у остальных null) */
  onboarding: Onboarding | null;
  /** Текст ошибки последней загрузки пути адаптации (null, если всё в порядке) */
  onboardingError: string | null;
  refreshOnboarding: () => Promise<void>;
  refreshApprovals: () => Promise<void>;
  approvalsCount: number;
  notify: (text: string, tone?: "error" | "ok") => void;
  go: (screen: Screen) => void;
  openRequest: (id: string) => void;
  selectedRequestId: string | null;
  /** Уведомления, открытые сбои, состояние хранилища */
  feed: Feed;
  /** Переход по уведомлению: заявка, очередь, стажер, стартовый набор */
  openNote: (n: Notification) => void;
  openStarterPack: () => void;
}

export const Ctx = createContext<WorkspaceCtx | null>(null);

export function useCtx(): WorkspaceCtx {
  const v = useContext(Ctx);
  if (!v) throw new Error("useCtx вне провайдера");
  return v;
}
