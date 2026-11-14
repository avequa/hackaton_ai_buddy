/**
 * Контракты API AI IT-Buddy для фронтенда: Zod-схемы, типы и клиент.
 * Источник правды - core/internal/httpapi/openapi.yaml (GET /api/openapi.yaml).
 *
 * Использование:
 *   const api = createApi({ baseUrl: "", getToken: () => store.token, onUnauthorized: () => logout() })
 *   const { token, user } = await api.login("anna.intern")
 *   const res = await api.chat({ message: "нужно поправить алерт", thread_id: threadId })
 *
 * Зависимость: zod (3.x или 4.x).
 */
import { z } from "zod";

// ---------- справочники ----------

export const Role = z.enum(["intern", "mentor", "lead", "sre"]);
export const Kind = z.enum(["access", "helpdesk", "equipment"]);
export const Level = z.enum(["read", "write", "admin"]);
export const Confirm = z.enum(["self", "mentor", "lead", "auto"]);
export const Verdict = z.enum(["ok", "needs_mentor"]);
export const Status = z.enum([
  "draft", "pending_mentor", "in_review", "approved", "rejected",
  "new", "in_progress", "ready", "waitlisted", "cancelled",
]);
export type Role = z.infer<typeof Role>;
export type Kind = z.infer<typeof Kind>;
export type Status = z.infer<typeof Status>;
export type Confirm = z.infer<typeof Confirm>;

/** Финальные статусы: после них заявку больше не опрашиваем */
export const FINAL_STATUSES: ReadonlySet<Status> = new Set(["approved", "rejected", "ready", "cancelled"]);

/** Подписи статусов для интерфейса */
export const STATUS_LABEL: Record<Status, string> = {
  draft: "Черновик",
  pending_mentor: "Ждет наставника",
  in_review: "На согласовании",
  new: "Зарегистрировано",
  in_progress: "В работе",
  waitlisted: "В листе ожидания",
  approved: "Одобрено",
  ready: "Готово к выдаче",
  rejected: "Отказ",
  cancelled: "Отменено",
};

/** Кто отмечает шаг адаптации */
export const CONFIRM_LABEL: Record<Confirm, string> = {
  self: "Отмечаешь сам",
  mentor: "Отмечает наставник",
  lead: "Отмечает руководитель",
  auto: "Закроется сам, когда выдадут доступ",
};

// ---------- сущности ----------

export const User = z.object({
  id: z.string(),
  name: z.string(),
  role: Role,
  position: z.string(),
  grade: z.string(),
  team: z.string(),
  mentor: z.string().optional(),
  lead: z.string().optional(),
  start_date: z.string().optional(),
});
export type User = z.infer<typeof User>;

export const HistoryEvent = z.object({
  at: z.string(),
  status: z.string(), // в истории бывает служебное значение, поэтому не enum
  by: z.string(),
  note: z.string().optional(),
});

export const Request = z.object({
  id: z.string(),
  kind: Kind,
  user: z.string(),
  status: Status,
  title: z.string(),
  policy: z.object({ verdict: Verdict, reasons: z.array(z.string()).optional() }),
  approver: z.string().optional(),
  approver_name: z.string().optional(),
  resolution: z.string().optional(),
  access: z.object({ service: z.string(), project: z.string(), level: Level, justification: z.string() }).optional(),
  ticket: z.object({
    service: z.string(),
    summary: z.string(),
    details: z.string().optional(),
    steps_tried: z.array(z.string()).optional(),
  }).optional(),
  equipment: z.object({
    action: z.enum(["issue", "replace", "return", "waitlist", "purchase"]),
    item: z.string(),
    model: z.string(),
    reason: z.string(),
  }).optional(),
  created_at: z.string(),
  updated_at: z.string(),
  history: z.array(HistoryEvent),
});
export type Request = z.infer<typeof Request>;

export const Grant = z.object({ service: z.string(), project: z.string(), level: Level });
export type Grant = z.infer<typeof Grant>;

export const Step = z.object({
  id: z.string(),
  week: z.number(),
  title: z.string(),
  confirm: Confirm,
  auto: z.object({ service: z.string() }).optional(),
  done: z.boolean(),
  done_by: z.string().optional(),
  hint: z.string().optional(),
});
export type Step = z.infer<typeof Step>;

export const Onboarding = z.object({
  user: z.string(),
  done: z.number(),
  total: z.number(),
  next: Step.optional(),
  steps: z.array(Step),
  open_requests: z.array(Request),
});
export type Onboarding = z.infer<typeof Onboarding>;

export const Mentee = z.object({
  user: User,
  done: z.number(),
  total: z.number(),
  next: Step.optional(),
  pending_approvals: z.number(),
});
export type Mentee = z.infer<typeof Mentee>;

export const NotificationKind = z.enum([
  "incident", "incident_resolved", "request", "approval", "security", "digest", "starter_pack",
]);
export type NotificationKind = z.infer<typeof NotificationKind>;

/** Уведомление в ленте чата. ref - id заявки (request, approval), инцидента (incident*) или стажера (security, digest) */
export const Notification = z.object({
  id: z.number(),
  kind: NotificationKind,
  title: z.string(),
  text: z.string(),
  ref: z.string().optional(),
  created_at: z.string(),
  read: z.boolean(),
});
export type Notification = z.infer<typeof Notification>;

export const Incident = z.object({
  id: z.string(),
  service: z.string(),
  title: z.string(),
  message: z.string().optional(),
  since: z.string(),
  by: z.string().optional(), // имя SRE или "incident-radar" - найден автоматически по логам
  resolved_at: z.string().optional(), // нет - инцидент открыт
});
export type Incident = z.infer<typeof Incident>;
export const RADAR = "incident-radar";

export const Readiness = z.object({
  status: z.enum(["ok", "degraded"]),
  storage: z.enum(["postgres", "memory"]),
  pending_changes: z.number(),
  last_flush_at: z.string().optional(),
  error: z.string().optional(),
});
export type Readiness = z.infer<typeof Readiness>;

export const SecurityEvent = z.object({
  id: z.number(),
  user: z.string(),
  name: z.string(),
  kind: z.enum(["foreign_object", "forbidden", "chat_attack"]),
  detail: z.string(),
  at: z.string(),
});
export type SecurityEvent = z.infer<typeof SecurityEvent>;

export const Digest = z.object({
  intern: User,
  week: z.number(),
  done: z.number(),
  total: z.number(),
  next: Step.optional(),
  overdue: z.array(Step),
  waiting_for_you: z.array(Request),
  steps_to_confirm: z.array(Step),
  stuck_requests: z.array(Request),
  rejected: z.array(Request),
  topics: z.array(z.object({ topic: z.string(), count: z.number() })),
  unanswered_in_kb: z.number(),
  security_events: z.array(SecurityEvent),
  talking_points: z.array(z.string()),
});
export type Digest = z.infer<typeof Digest>;

export const StarterItem = z.object({
  service: z.string(),
  project: z.string(),
  level: z.string(),
  outcome: z.enum(["draft_created", "already_granted", "already_requested"]),
  request: Request.optional(),
});

// ---------- ответы эндпоинтов ----------

export const LoginResponse = z.object({ token: z.string(), user: User, expires_in: z.number() });
export const ChatRequest = z.object({ message: z.string().min(1).max(4000), thread_id: z.string().optional() });
export const ChatResponse = z.object({
  reply: z.string(),
  tools_used: z.array(z.string()),
  drafts: z.array(Request),
  thread_id: z.string(),
});
export type ChatRequest = z.infer<typeof ChatRequest>;
export type ChatResponse = z.infer<typeof ChatResponse>;

const Personas = z.object({ personas: z.array(User) });
const Requests = z.object({ requests: z.array(Request) });
const Access = z.object({ grants: z.array(Grant), open_requests: z.array(Request) });
const Mentees = z.object({ mentees: z.array(Mentee) });
const Approvals = z.object({ approvals: z.array(Request) });
const ErrorBody = z.object({ error: z.string() });
const Notifications = z.object({ notifications: z.array(Notification), unread: z.number() });
const Incidents = z.object({ incidents: z.array(Incident) });
const SecurityEvents = z.object({ events: z.array(SecurityEvent) });
const StarterPack = z.object({ items: z.array(StarterItem), draft_ids: z.array(z.string()) });
const ConfirmBatch = z.object({
  results: z.array(z.object({ id: z.string(), request: Request.optional(), error: z.string().optional() })),
});

// ---------- клиент ----------

/** Ошибка API: status - HTTP-код, message - текст из {"error"}, его можно показывать пользователю */
export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ApiOptions {
  /** "" при прокси Vite (/api -> 127.0.0.1:8080), иначе "http://127.0.0.1:8080" */
  baseUrl: string;
  getToken: () => string | null | undefined;
  /** 401: токена нет или истек - вернуть на экран входа */
  onUnauthorized?: () => void;
}

export function createApi(opts: ApiOptions) {
  async function call<T>(schema: z.ZodType<T>, method: "GET" | "POST", path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {};
    const token = opts.getToken();
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await fetch(opts.baseUrl + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const data: unknown = await res.json().catch(() => ({}));
    if (!res.ok) {
      if (res.status === 401) opts.onUnauthorized?.();
      const parsed = ErrorBody.safeParse(data);
      throw new ApiError(res.status, parsed.success ? parsed.data.error : `HTTP ${res.status}`);
    }
    return schema.parse(data);
  }
  const id = encodeURIComponent;

  return {
    personas: () => call(Personas, "GET", "/api/personas").then((r) => r.personas),
    login: (userId: string) => call(LoginResponse, "POST", "/api/auth/login", { user_id: userId }),
    me: () => call(User, "GET", "/api/me"),

    /** Синхронный ход диалога: 3-15 с, бывает дольше. 502 - помощник недоступен, текст ошибки можно показать */
    chat: (req: ChatRequest) => call(ChatResponse, "POST", "/api/chat", req),

    requests: (kind?: Kind) => call(Requests, "GET", `/api/requests${kind ? `?kind=${kind}` : ""}`).then((r) => r.requests),
    request: (reqId: string) => call(Request, "GET", `/api/requests/${id(reqId)}`),
    /** 409 - заявка уже не черновик или позиция закончилась на складе */
    confirm: (reqId: string) => call(Request, "POST", `/api/requests/${id(reqId)}/confirm`),
    cancel: (reqId: string) => call(Request, "POST", `/api/requests/${id(reqId)}/cancel`),

    access: () => call(Access, "GET", "/api/access"),
    onboarding: () => call(Onboarding, "GET", "/api/onboarding"),
    /** Только шаги с confirm = "self", иначе 403 */
    completeStep: (stepId: string) => call(Step, "POST", `/api/onboarding/steps/${id(stepId)}/complete`),

    mentees: () => call(Mentees, "GET", "/api/mentees").then((r) => r.mentees),
    menteeOnboarding: (userId: string) => call(Onboarding, "GET", `/api/mentees/${id(userId)}/onboarding`),
    completeMenteeStep: (userId: string, stepId: string) =>
      call(Step, "POST", `/api/mentees/${id(userId)}/steps/${id(stepId)}/complete`),

    /** Лента для окна чата: опрашивать раз в 3-5 с, after = id последнего полученного */
    notifications: (after = 0) => call(Notifications, "GET", `/api/notifications?after=${after}`),
    markRead: (upTo: number) => call(z.object({ marked: z.number() }), "POST", "/api/notifications/read", { up_to: upTo }),

    incidents: () => call(Incidents, "GET", "/api/incidents").then((r) => r.incidents),
    /** Только роль sre или lead, иначе 403. 409 - по сервису уже открыт инцидент */
    openIncident: (body: { service: string; title: string; message?: string }) => call(Incident, "POST", "/api/incidents", body),
    resolveIncident: (incId: string) => call(Incident, "POST", `/api/incidents/${id(incId)}/resolve`),

    /** Черновики стандартных доступов стажера; затем confirmBatch(draft_ids) */
    starterPack: () => call(StarterPack, "POST", "/api/onboarding/starter-pack"),
    /** Ошибка одной заявки не отменяет остальные - смотри results[].error */
    confirmBatch: (ids: string[]) => call(ConfirmBatch, "POST", "/api/requests/confirm-batch", { ids }),

    digest: (userId: string) => call(Digest, "GET", `/api/mentees/${id(userId)}/digest`),
    securityEvents: () => call(SecurityEvents, "GET", "/api/security/events").then((r) => r.events),

    approvals: () => call(Approvals, "GET", "/api/approvals").then((r) => r.approvals),
    /** comment при отказе стажер увидит как причину */
    decide: (reqId: string, decision: "approve" | "reject", comment?: string) =>
      call(Request, "POST", `/api/approvals/${id(reqId)}/${decision}`, comment ? { comment } : undefined),
  };
}

export type Api = ReturnType<typeof createApi>;

/** Есть ли заявки, статус которых еще изменится: пока true - опрашивать requests() раз в 3-5 с */
export const hasPending = (items: Request[]) => items.some((r) => r.status !== "draft" && !FINAL_STATUSES.has(r.status));
