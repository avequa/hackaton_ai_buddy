/**
 * Поддельный бэкенд для тестов интерфейса: те же адреса и формат ответов, что у ядра
 * (схемы в src/api.ts проверяют каждый ответ). Состояние в памяти, сбрасывается в каждом тесте.
 */
import { vi } from "vitest";
import type { Incident, Notification, Request, Step, User } from "../src/api";

const NOW = "2026-11-15T10:00:00Z";

export const users: User[] = [
  { id: "anna.intern", name: "Анна Смирнова", role: "intern", position: "backend", grade: "intern", team: "payments", mentor: "igor.mentor", lead: "olga.lead" },
  { id: "pavel.intern", name: "Павел Орлов", role: "intern", position: "frontend", grade: "intern", team: "cabinet", mentor: "maria.mentor", lead: "olga.lead" },
  { id: "igor.mentor", name: "Игорь Ковалев", role: "mentor", position: "backend", grade: "senior", team: "payments" },
  { id: "olga.lead", name: "Ольга Новикова", role: "lead", position: "backend", grade: "lead", team: "payments" },
  { id: "sergey.sre", name: "Сергей Волков", role: "sre", position: "sre", grade: "middle", team: "platform" },
];

function stepsFor(): Step[] {
  return [
    { id: "meet-team", week: 1, title: "Познакомиться с командой", confirm: "self", done: false, hint: "отметь сам, когда сделаешь" },
    { id: "goals", week: 1, title: "Обсудить цели с руководителем", confirm: "lead", done: false, hint: "отмечает руководитель" },
    { id: "vpn", week: 1, title: "Подключиться к VPN", confirm: "auto", auto: { service: "vpn" }, done: true, done_by: "система: доступ выдан" },
    { id: "setup-env", week: 1, title: "Настроить окружение", confirm: "mentor", done: false, hint: "отмечает наставник" },
    { id: "grafana", week: 2, title: "Получить доступ к Grafana", confirm: "auto", auto: { service: "grafana" }, done: false },
  ];
}

export interface FakeState {
  requests: Request[];
  notes: Notification[];
  incidents: Incident[];
  steps: Record<string, Step[]>;
  degraded: boolean;
  chatFails: boolean;
  calls: string[];
}

let seq = 1000;
export function makeRequest(over: Partial<Request> & { user: string }): Request {
  seq += 1;
  return {
    id: `ACC-${seq}`,
    kind: "access",
    status: "draft",
    title: "Доступ write к Grafana / Дашборды платежей",
    policy: { verdict: "ok" },
    approver: "igor.mentor",
    approver_name: "Игорь Ковалев",
    access: { service: "grafana", project: "payments-dashboards", level: "write", justification: "поправить алерт" },
    created_at: NOW,
    updated_at: NOW,
    history: [{ at: NOW, status: "draft", by: "anna.intern", note: "черновик" }],
    ...over,
  };
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

export function installFakeBackend(init?: Partial<FakeState>) {
  seq = 1000;
  const st: FakeState = {
    requests: [],
    notes: [],
    incidents: [],
    steps: { "anna.intern": stepsFor(), "pavel.intern": stepsFor() },
    degraded: false,
    chatFails: false,
    calls: [],
    ...init,
  };
  let noteSeq = st.notes.reduce((m, n) => Math.max(m, n.id), 0);
  const note = (user: string, kind: Notification["kind"], title: string, text = "", ref?: string) => {
    noteSeq += 1;
    st.notes.push({ id: noteSeq, user, kind, title, text, ref, created_at: NOW, read: false } as Notification & { user: string });
  };
  const onboarding = (u: string) => {
    const steps = st.steps[u] ?? stepsFor();
    const done = steps.filter((s) => s.done).length;
    return { user: u, done, total: steps.length, next: steps.find((s) => !s.done), steps, open_requests: st.requests.filter((r) => r.user === u && r.status !== "approved") };
  };

  const handler = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    const path = url.pathname;
    st.calls.push(`${method} ${path}`);
    const auth = new Headers(init?.headers).get("Authorization") ?? "";
    const me = users.find((u) => auth === `Bearer t:${u.id}`);
    const body = init?.body ? JSON.parse(String(init.body)) : {};
    const seg = path.split("/").filter(Boolean);

    if (path === "/readyz") return json(200, { status: st.degraded ? "degraded" : "ok", storage: "postgres", pending_changes: st.degraded ? 3 : 0 });
    if (path === "/api/personas") return json(200, { personas: users });
    if (path === "/api/auth/login") {
      const u = users.find((x) => x.id === body.user_id);
      return u ? json(200, { token: `t:${u.id}`, user: u, expires_in: 43200 }) : json(404, { error: "нет такого сотрудника" });
    }
    if (!me) return json(401, { error: "нужен вход" });
    const mine = (r: Request) => r.user === me.id;

    if (path === "/api/me") return json(200, me);
    if (path === "/api/chat") {
      if (st.chatFails) return json(502, { error: "Помощник сейчас недоступен, попробуй еще раз через минуту" });
      const tools: string[] = ["list_services", "list_my_access"];
      let reply = "Привет! Чем помочь?";
      if (String(body.message).includes("алерт")) {
        const r = makeRequest({ user: me.id });
        st.requests.unshift(r);
        tools.push("draft_access_request");
        reply = `Нужна **Grafana**, уровень \`write\`:\n- проект payments-dashboards\n- согласует Игорь\n\nПодготовил черновик ${r.id}.`;
      }
      return json(200, { reply, tools_used: tools, drafts: st.requests.filter((r) => mine(r) && r.status === "draft"), thread_id: body.thread_id ?? "t1" });
    }
    if (path === "/api/requests" && method === "GET") {
      const kind = url.searchParams.get("kind");
      return json(200, { requests: st.requests.filter((r) => mine(r) && (!kind || r.kind === kind)) });
    }
    if (path === "/api/requests/confirm-batch") {
      return json(200, {
        results: (body.ids as string[]).map((id) => {
          const r = st.requests.find((x) => x.id === id && mine(x));
          if (!r) return { id, error: "не найдено" };
          r.status = "approved";
          return { id, request: r };
        }),
      });
    }
    if (seg[0] === "api" && seg[1] === "requests" && seg[2]) {
      const r = st.requests.find((x) => x.id === seg[2] && mine(x));
      if (!r) return json(404, { error: "not found" });
      if (seg[3] === "confirm") {
        if (r.status !== "draft") return json(409, { error: "заявка уже не черновик" });
        r.status = r.policy.verdict === "needs_mentor" ? "pending_mentor" : "in_review";
        r.history.push({ at: NOW, status: r.status, by: me.id });
      } else if (seg[3] === "cancel") {
        r.status = "cancelled";
      }
      return json(200, r);
    }
    if (path === "/api/access") {
      return json(200, { grants: [{ service: "vpn", project: "corp", level: "read" }], open_requests: st.requests.filter((r) => mine(r) && r.status !== "approved") });
    }
    if (path === "/api/onboarding") return json(200, onboarding(me.id));
    if (seg[1] === "onboarding" && seg[2] === "steps") {
      const s = st.steps[me.id].find((x) => x.id === seg[3]);
      if (!s || s.confirm !== "self") return json(403, { error: "этот шаг отмечает не стажер" });
      s.done = true;
      return json(200, s);
    }
    if (path === "/api/onboarding/starter-pack") {
      const a = makeRequest({ user: me.id, title: "Доступ read к GitLab", access: { service: "gitlab", project: "cabinet-web", level: "read", justification: "старт" } });
      const b = makeRequest({ user: me.id, title: "Доступ read к Jira", access: { service: "jira", project: "CAB", level: "read", justification: "старт" } });
      st.requests.push(a, b);
      return json(200, {
        items: [
          { service: "vpn", project: "corp", level: "read", outcome: "already_granted" },
          { service: "gitlab", project: "cabinet-web", level: "read", outcome: "draft_created", request: a },
          { service: "jira", project: "CAB", level: "read", outcome: "draft_created", request: b },
          { service: "confluence", project: "cabinet-space", level: "read", outcome: "already_requested" },
        ],
        draft_ids: [a.id, b.id],
      });
    }
    if (path === "/api/mentees") {
      if (me.role === "intern") return json(403, { error: "forbidden" });
      return json(200, { mentees: [{ user: users[0], done: 1, total: 5, next: stepsFor()[0], pending_approvals: 1 }] });
    }
    if (seg[1] === "mentees" && seg[3] === "onboarding") return json(200, onboarding(seg[2]));
    if (seg[1] === "mentees" && seg[3] === "steps") {
      const s = st.steps[seg[2]].find((x) => x.id === seg[4]);
      if (!s) return json(404, { error: "not found" });
      s.done = true;
      return json(200, s);
    }
    if (seg[1] === "mentees" && seg[3] === "digest") {
      return json(200, {
        intern: users[0], week: 2, done: 1, total: 5, next: stepsFor()[0], overdue: [], waiting_for_you: [], steps_to_confirm: [],
        stuck_requests: [], rejected: [], topics: [{ topic: "VPN", count: 3 }], unanswered_in_kb: 2,
        security_events: [{ id: 1, user: "anna.intern", name: "Анна Смирнова", kind: "chat_attack", detail: "просит игнорировать инструкции", at: NOW }],
        talking_points: ["Обсудить VPN: стажер приходил с этим 3 раза", "Попытки выйти за права: 1 - объяснить, как устроены доступы"],
      });
    }
    if (path === "/api/security/events") {
      return json(200, { events: [{ id: 1, user: "anna.intern", name: "Анна Смирнова", kind: "chat_attack", detail: "просит игнорировать инструкции", at: NOW }] });
    }
    if (path === "/api/approvals") return json(200, { approvals: st.requests.filter((r) => r.status === "pending_mentor") });
    if (seg[1] === "approvals" && seg[2]) {
      const r = st.requests.find((x) => x.id === seg[2]);
      if (!r) return json(404, { error: "not found" });
      r.status = seg[3] === "approve" ? "in_review" : "rejected";
      r.resolution = body.comment;
      note(r.user, "request", `${r.id}: ${r.status}`, body.comment ?? "", r.id);
      return json(200, r);
    }
    if (path === "/api/notifications" && method === "GET") {
      const after = Number(url.searchParams.get("after") ?? 0);
      const list = st.notes.filter((n) => (n as Notification & { user?: string }).user === me.id || !(n as { user?: string }).user);
      return json(200, {
        notifications: list.filter((n) => n.id > after).map(({ ...n }) => { delete (n as { user?: string }).user; return n; }),
        unread: list.filter((n) => !n.read).length,
      });
    }
    if (path === "/api/notifications/read") {
      st.notes.forEach((n) => n.id <= body.up_to && (n.read = true));
      return json(200, { marked: 1 });
    }
    if (path === "/api/incidents" && method === "GET") return json(200, { incidents: st.incidents });
    if (path === "/api/incidents" && method === "POST") {
      if (me.role !== "sre" && me.role !== "lead") return json(403, { error: "только SRE" });
      if (st.incidents.some((i) => i.service === body.service && !i.resolved_at)) return json(409, { error: "уже открыт" });
      const inc: Incident = { id: `INC-${100 + st.incidents.length + 1}`, service: body.service, title: body.title, message: body.message, since: NOW, by: me.id };
      st.incidents.push(inc);
      return json(200, inc);
    }
    if (seg[1] === "incidents" && seg[3] === "resolve") {
      const inc = st.incidents.find((i) => i.id === seg[2]);
      if (!inc) return json(404, { error: "not found" });
      inc.resolved_at = NOW;
      return json(200, inc);
    }
    return json(404, { error: `fake: нет обработчика ${method} ${path}` });
  });

  vi.stubGlobal("fetch", handler);
  return { state: st, fetch: handler, note };
}
