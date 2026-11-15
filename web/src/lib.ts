import {
  ApiError,
  FINAL_STATUSES,
  createApi,
  type Kind,
  type Grant,
  type Request as Req,
  type Role,
  type Status,
  type User,
} from "./api";
import type { IconName } from "./Icon";

export const TOKEN_KEY = "itbuddy.token";
export const UNAUTH_EVENT = "itbuddy:unauthorized";

/** Один экземпляр клиента на всё приложение. Токен читается из localStorage при каждом запросе. */
export const api = createApi({
  // "" = ходим на тот же origin, /api проксируется Vite (см. vite.config.ts).
  // Для прямого доступа задайте VITE_API_URL=http://127.0.0.1:8080
  baseUrl: (import.meta.env.VITE_API_URL as string | undefined) ?? "",
  getToken: () => localStorage.getItem(TOKEN_KEY),
  onUnauthorized: () => window.dispatchEvent(new Event(UNAUTH_EVENT)),
});

export function errText(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof TypeError) return "Нет связи с сервером. Проверьте, что ядро запущено.";
  if (e instanceof Error) return e.message;
  return "Что-то пошло не так";
}

export const uid = () => Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
export const newThreadId = () =>
  typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : uid();

// ---------- подписи ----------

export const ROLE_LABEL: Record<Role, string> = {
  intern: "Стажёр",
  mentor: "Наставник",
  lead: "Руководитель",
  sre: "SRE",
};

export const KIND_LABEL: Record<Kind, string> = {
  access: "Доступы",
  helpdesk: "Техподдержка",
  equipment: "Оборудование",
};

export const KIND_ICON: Record<Kind, IconName> = {
  access: "shield",
  helpdesk: "headphones",
  equipment: "laptop",
};

export const LEVEL_LABEL: Record<Grant["level"], string> = {
  read: "Чтение",
  write: "Запись",
  admin: "Администратор",
};

export const EQUIPMENT_ACTION_LABEL: Record<string, string> = {
  issue: "Выдача",
  replace: "Замена",
  return: "Возврат",
  waitlist: "Лист ожидания",
  purchase: "Закупка",
};

/** Человекочитаемые названия инструментов агента (неизвестные показываем как есть) */
const TOOL_LABEL: Record<string, string> = {
  list_services: "каталог сервисов",
  list_my_access: "проверил доступы",
  list_access_requests: "посмотрел заявки на доступ",
  draft_access_request: "создал черновик заявки на доступ",
  check_service_health: "проверил, нет ли сбоя",
  search_kb: "база знаний",
  draft_ticket: "создал черновик обращения",
  list_tickets: "посмотрел обращения",
  list_equipment: "проверил склад",
  list_equipment_requests: "посмотрел заявки на оборудование",
  draft_equipment_request: "создал черновик заявки на оборудование",
  get_onboarding: "путь адаптации",
};
export const toolLabel = (name: string) => TOOL_LABEL[name] ?? name;

export type Tone = "gray" | "amber" | "blue" | "violet" | "green" | "red";

export const STATUS_TONE: Record<Status, Tone> = {
  draft: "gray",
  pending_mentor: "amber",
  in_review: "amber",
  new: "blue",
  in_progress: "blue",
  waitlisted: "violet",
  approved: "green",
  ready: "green",
  rejected: "red",
  cancelled: "gray",
};

export const isFinal = (s: Status) => FINAL_STATUSES.has(s);
/** Заявка ещё «живая»: не в финальном статусе (черновики тоже считаем) */
export const isOpen = (r: Req) => !isFinal(r.status);
/** Отменить можно, пока нет решения */
export const canCancel = (r: Req) => !isFinal(r.status);

// ---------- имена, даты ----------

export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  return (parts.length > 1 ? parts[0][0] + parts[1][0] : name.slice(0, 2)).toUpperCase();
}

export const firstName = (name: string) => name.trim().split(/\s+/)[0] ?? name;

export function greeting(date = new Date()): string {
  const h = date.getHours();
  if (h >= 5 && h < 12) return "Доброе утро";
  if (h >= 12 && h < 18) return "Добрый день";
  if (h >= 18 && h < 23) return "Добрый вечер";
  return "Доброй ночи";
}

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

export function fmtTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
}

export function fmtDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const diff = Math.round((startOfDay(new Date()) - startOfDay(d)) / 86_400_000);
  const time = fmtTime(iso);
  if (diff === 0) return `Сегодня, ${time}`;
  if (diff === 1) return `Вчера, ${time}`;
  return `${d.toLocaleDateString("ru-RU", { day: "numeric", month: "long" })}, ${time}`;
}

export const requestsSignature = (items: Req[]) => items.map((r) => `${r.id}:${r.status}`).join("|");

export type { User };

// ---------- сервисы, уведомления, сбои ----------

export const SERVICE_LABEL: Record<string, string> = {
  vpn: "VPN",
  gitlab: "GitLab",
  jira: "Jira",
  confluence: "Confluence",
  grafana: "Grafana",
  "k8s-prod": "Kubernetes (prod)",
};
export const serviceLabel = (id: string) => SERVICE_LABEL[id] ?? id;

export const NOTE_META: Record<string, { icon: IconName; tone: Tone; label: string }> = {
  incident: { icon: "alert", tone: "red", label: "Сбой" },
  incident_resolved: { icon: "check", tone: "green", label: "Восстановлено" },
  request: { icon: "file", tone: "blue", label: "Заявка" },
  approval: { icon: "users", tone: "amber", label: "Нужно решение" },
  security: { icon: "shield", tone: "red", label: "Безопасность" },
  digest: { icon: "users", tone: "violet", label: "Сводка" },
  starter_pack: { icon: "key", tone: "blue", label: "Стартовый набор" },
};

export const SECURITY_KIND_LABEL: Record<string, string> = {
  foreign_object: "чужие данные",
  forbidden: "действие не по роли",
  chat_attack: "попытка обойти правила",
};

export function fmtAgo(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return "только что";
  if (s < 3600) return `${Math.floor(s / 60)} мин назад`;
  if (s < 86400) return `${Math.floor(s / 3600)} ч назад`;
  return fmtDate(iso);
}
