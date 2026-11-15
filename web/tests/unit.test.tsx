import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiError, createApi, hasPending } from "../src/api";
import { Markdown } from "../src/Markdown";
import { errText, firstName, fmtAgo, fmtDate, fmtTime, greeting, initials, isOpen, requestsSignature, serviceLabel, toolLabel } from "../src/lib";
import { matchRoute, paths, safeNext } from "../src/routes";
import { makeRequest } from "./fakeBackend";

describe("Markdown", () => {
  it("рендерит жирный, код, списки и абзацы без HTML", () => {
    const { container } = render(
      <Markdown text={"# Итог\nНужна **Grafana**, уровень `write`.\nВторая строка\n\n- один\n  продолжение\n- два\n\n1. первый\n2. второй\n<b>не тег</b>"} />,
    );
    expect(container.querySelectorAll("strong")).toHaveLength(2);
    expect(container.querySelector("code")?.textContent).toBe("write");
    expect(container.querySelectorAll("ul li")).toHaveLength(2);
    expect(container.querySelector("ul li")?.textContent).toBe("один продолжение");
    expect(container.querySelectorAll("ol li")).toHaveLength(2);
    expect(container.querySelector("br")).not.toBeNull();
    expect(container.querySelector("b")).toBeNull(); // текст модели не становится разметкой
    expect(container.textContent).toContain("<b>не тег</b>");
  });
});

describe("lib", () => {
  it("форматирует и подписывает", () => {
    expect(initials("Анна Смирнова")).toBe("АС");
    expect(firstName("Анна Смирнова")).toBe("Анна");
    expect(serviceLabel("k8s-prod")).toBe("Kubernetes (prod)");
    expect(serviceLabel("mail")).toBe("mail");
    expect(toolLabel("check_service_health")).toBe("проверил, нет ли сбоя");
    expect(toolLabel("unknown_tool")).toBe("unknown_tool");
    expect(greeting(new Date(2026, 0, 1, 9))).toMatch(/утро/i);
    expect(greeting(new Date(2026, 0, 1, 15))).toMatch(/день/i);
    expect(greeting(new Date(2026, 0, 1, 20))).toMatch(/вечер/i);
    expect(greeting(new Date(2026, 0, 1, 2))).toMatch(/ночи|вечер|ноч/i);
    expect(fmtTime("2026-11-15T10:05:00Z")).toMatch(/\d{2}:\d{2}/);
    expect(fmtDate(new Date().toISOString())).toMatch(/Сегодня/);
    expect(fmtDate("2026-01-02T10:00:00Z")).not.toBe("");
    const now = Date.parse("2026-11-15T10:00:00Z");
    expect(fmtAgo("2026-11-15T09:59:50Z", now)).toBe("только что");
    expect(fmtAgo("2026-11-15T09:50:00Z", now)).toBe("10 мин назад");
    expect(fmtAgo("2026-11-15T07:00:00Z", now)).toBe("3 ч назад");
    expect(fmtAgo("2026-11-10T07:00:00Z", now)).not.toMatch(/назад/);
  });

  it("статусы и ошибки", () => {
    const draft = makeRequest({ user: "a" });
    const done = makeRequest({ user: "a", status: "approved" });
    const review = makeRequest({ user: "a", status: "in_review" });
    expect(isOpen(draft)).toBe(true);
    expect(isOpen(done)).toBe(false);
    expect(hasPending([draft, done])).toBe(false);
    expect(hasPending([review])).toBe(true);
    expect(requestsSignature([draft, done])).toBe(`${draft.id}:draft|${done.id}:approved`);
    expect(errText(new ApiError(409, "уже не черновик"))).toBe("уже не черновик");
    expect(errText(new TypeError("fetch failed"))).toMatch(/Нет связи/);
    expect(errText(new Error("x"))).toBe("x");
    expect(errText(42)).toMatch(/не так/);
  });
});

describe("routes", () => {
  it("разбирает адреса", () => {
    expect(matchRoute("/")).toEqual({ screen: "root" });
    expect(matchRoute("/chat/access")).toEqual({ screen: "chat", moduleId: "access" });
    expect(matchRoute("/chat")).toEqual({ screen: "chat" });
    expect(matchRoute("/chat/nope")).toEqual({ screen: "notfound" });
    expect(matchRoute("/requests/ACC%201")).toEqual({ screen: "requests", requestId: "ACC 1" });
    expect(matchRoute("/requests/%E0%A4%A")).toEqual({ screen: "requests", requestId: "%E0%A4%A" });
    expect(matchRoute("/mentees/anna.intern")).toEqual({ screen: "mentees", menteeId: "anna.intern" });
    expect(matchRoute("/incidents")).toEqual({ screen: "incidents" });
    for (const p of ["/login", "/modules", "/onboarding", "/access", "/approvals"]) expect(matchRoute(p).screen).toBe(p.slice(1));
    for (const p of ["/login/x", "/modules/x", "/onboarding/x", "/access/x", "/approvals/x", "/incidents/x", "/a/b/c", "/x", "/x/y"]) {
      expect(matchRoute(p)).toEqual({ screen: "notfound" });
    }
    expect(paths.chat()).toBe("/chat");
  });
  it("не уводит на чужой сайт после входа", () => {
    expect(safeNext("/requests")).toBe("/requests");
    for (const bad of [null, "", "https://evil", "//evil", "/\\evil", "/login?next=/x"]) expect(safeNext(bad)).toBeNull();
  });
});

describe("api client", () => {
  it("проверяет ответ схемой и превращает ошибки в ApiError", async () => {
    const onUnauthorized = vi.fn();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: "нужен вход" }), { status: 401 }))
      .mockResolvedValueOnce(new Response("oops", { status: 500 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ personas: [{ id: 1 }] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ marked: 2 }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const api = createApi({ baseUrl: "http://core", getToken: () => "tok", onUnauthorized });
    await expect(api.me()).rejects.toMatchObject({ status: 401, message: "нужен вход" });
    expect(onUnauthorized).toHaveBeenCalledOnce();
    await expect(api.requests("access")).rejects.toMatchObject({ status: 500, message: "HTTP 500" });
    await expect(api.personas()).rejects.toThrow(); // ответ не по схеме
    await expect(api.markRead(5)).resolves.toEqual({ marked: 2 });
    const [url, init] = fetchMock.mock.calls[3];
    expect(url).toBe("http://core/api/notifications/read");
    expect(init.headers).toMatchObject({ Authorization: "Bearer tok", "Content-Type": "application/json" });
    expect(fetchMock.mock.calls[1][0]).toBe("http://core/api/requests?kind=access");
  });

  it("покрывает все методы клиента", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
      calls.push(`${init.method} ${url}`);
      return new Response("{}", { status: 418 });
    }));
    const api = createApi({ baseUrl: "", getToken: () => null });
    const all = [
      api.personas(), api.login("a"), api.me(), api.chat({ message: "x" }), api.requests(), api.request("A 1"), api.confirm("A"),
      api.cancel("A"), api.access(), api.onboarding(), api.completeStep("s"), api.mentees(), api.menteeOnboarding("u"),
      api.completeMenteeStep("u", "s"), api.notifications(), api.markRead(1), api.incidents(), api.openIncident({ service: "vpn", title: "t" }),
      api.resolveIncident("I"), api.starterPack(), api.confirmBatch(["A"]), api.digest("u"), api.securityEvents(), api.approvals(),
      api.decide("A", "reject", "нет"), api.decide("A", "approve"),
    ];
    const res = await Promise.allSettled(all);
    expect(res.every((r) => r.status === "rejected")).toBe(true);
    expect(calls).toContain("GET /api/requests/A%201");
    expect(calls).toContain("POST /api/approvals/A/reject");
  });
});
