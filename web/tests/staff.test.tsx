import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { installFakeBackend, makeRequest } from "./fakeBackend";
import { openApp } from "./render";

describe("наставник", () => {
  it("стажеры, тревоги, сводка 1:1, отметить шаг", async () => {
    const { state } = installFakeBackend();
    const { user } = await openApp("/mentees", "igor.mentor");
    expect(await screen.findByText("Попытки выйти за права")).toBeInTheDocument();
    expect(screen.getByText("попытка обойти правила")).toBeInTheDocument();
    expect(screen.getByText("Ждут решения: 1")).toBeInTheDocument();
    // клик по тревоге открывает стажера
    await user.click(screen.getByText("просит игнорировать инструкции"));
    expect(window.location.pathname).toBe("/mentees/anna.intern");
    expect(await screen.findByText("Сводка к встрече 1:1")).toBeInTheDocument();
    expect(screen.getByText(/Обсудить VPN/)).toBeInTheDocument();
    expect(screen.getByText("VPN × 3")).toBeInTheDocument();
    expect(screen.getByText(/не нашлось в базе знаний: 2/)).toBeInTheDocument();
    // наставник отмечает шаг «Настроить окружение», шаг руководителя ему недоступен
    const ready = await screen.findAllByRole("button", { name: "Готово" });
    expect(ready).toHaveLength(1);
    await user.click(ready[0]);
    await waitFor(() => expect(state.steps["anna.intern"].find((s) => s.id === "setup-env")?.done).toBe(true));
    await user.click(screen.getByRole("button", { name: /Все стажёры/ }));
    expect(window.location.pathname).toBe("/mentees");
  });

  it("сводка без тем и без тревог", async () => {
    const { fetch } = installFakeBackend();
    const real = fetch.getMockImplementation()!;
    fetch.mockImplementation(async (input, init) => {
      if (String(input).endsWith("/digest")) {
        const res = await real(input, init);
        const d = await res.json();
        return new Response(JSON.stringify({ ...d, talking_points: [], topics: [], unanswered_in_kb: 0, security_events: [] }), { status: 200 });
      }
      return real(input, init);
    });
    await openApp("/mentees/anna.intern", "igor.mentor");
    expect(await screen.findByText(/особых тем для разговора нет/)).toBeInTheDocument();
  });

  it("согласования: отклонить с причиной и одобрить", async () => {
    const { state } = installFakeBackend();
    state.requests.push(
      makeRequest({ user: "anna.intern", status: "pending_mentor", title: "Доступ read к Kubernetes (prod)", policy: { verdict: "needs_mentor", reasons: ["не типичен для роли"] } }),
      makeRequest({ user: "anna.intern", status: "pending_mentor", title: "Доступ write к GitLab" }),
    );
    const { user } = await openApp("/approvals", "igor.mentor");
    const card = (await screen.findByText("Доступ read к Kubernetes (prod)")).closest("article")!;
    await user.type(within(card as HTMLElement).getByPlaceholderText(/Комментарий/), "Логи смотри в Grafana");
    await user.click(within(card as HTMLElement).getByRole("button", { name: "Отклонить" }));
    await waitFor(() => expect(state.requests[0].status).toBe("rejected"));
    expect(state.requests[0].resolution).toBe("Логи смотри в Grafana");
    expect(await screen.findByText(/отклонена/)).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: /Одобрить/ }));
    await waitFor(() => expect(state.requests[1].status).toBe("in_review"));
    expect(await screen.findByText("Очередь пуста")).toBeInTheDocument();
  });

  it("уведомление о тревоге ведет к стажеру, о решении - в очередь", async () => {
    const { note } = installFakeBackend();
    note("igor.mentor", "security", "Попытка обойти ограничения", "Анна", "anna.intern");
    note("igor.mentor", "approval", "Нужно решение", "ACC-1", "ACC-1");
    note("igor.mentor", "digest", "Сводка по стажеру", "", "anna.intern");
    const { user } = await openApp("/modules", "igor.mentor");
    const bell = await screen.findByRole("button", { name: "Уведомления" });
    await waitFor(() => expect(bell).toHaveTextContent("3"));
    await user.click(bell);
    await user.click(screen.getByText("Нужно решение", { selector: "strong" }));
    expect(window.location.pathname).toBe("/approvals");
    await user.click(screen.getByRole("button", { name: "Уведомления" }));
    await user.click(screen.getByText("Попытка обойти ограничения"));
    expect(window.location.pathname).toBe("/mentees/anna.intern");
    // клик мимо панели закрывает ее
    await user.click(screen.getByRole("button", { name: "Уведомления" }));
    expect(screen.getByRole("dialog", { name: "Уведомления" })).toBeInTheDocument();
    await user.click(document.body);
    expect(screen.queryByRole("dialog", { name: "Уведомления" })).toBeNull();
  });

  it("наставника не пускает на путь адаптации и сбои", async () => {
    installFakeBackend();
    await openApp("/onboarding", "igor.mentor");
    await waitFor(() => expect(window.location.pathname).toBe("/modules"));
    expect(screen.queryByText("Сбои")).toBeNull();
  });
});

describe("SRE и руководитель", () => {
  it("SRE объявляет сбой, видит баннер и закрывает его", async () => {
    const { state } = installFakeBackend();
    const { user } = await openApp("/incidents", "sergey.sre");
    expect(await screen.findByText("Сбоев нет")).toBeInTheDocument();
    await user.selectOptions(screen.getByRole("combobox"), "gitlab");
    await user.type(screen.getByPlaceholderText(/Недоступен шлюз/), "Не работает CI");
    await user.type(screen.getByPlaceholderText(/резервный шлюз/), "Перезапускать пайплайны позже");
    await user.click(screen.getByRole("button", { name: /Объявить и уведомить всех/ }));
    await waitFor(() => expect(state.incidents).toHaveLength(1));
    expect(await screen.findByText(/INC-101 объявлен/)).toBeInTheDocument();
    expect(await screen.findByText(/объявил дежурный/)).toBeInTheDocument();
    // повторное объявление - ошибка с сервера
    await user.type(screen.getByPlaceholderText(/Недоступен шлюз/), "Еще раз");
    await user.click(screen.getByRole("button", { name: /Объявить и уведомить всех/ }));
    expect(await screen.findByText("уже открыт")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Закрыть" }));
    await waitFor(() => expect(state.incidents[0].resolved_at).toBeTruthy());
    expect(await screen.findByText("Недавно закрытые")).toBeInTheDocument();
  });

  it("руководитель видит и стажеров, и сбои; инцидент от радара подписан", async () => {
    installFakeBackend({
      incidents: [{ id: "INC-7", service: "vpn", title: "60% запросов с 5xx", since: "2026-11-15T10:00:00Z", by: "incident-radar" }],
    });
    const { user } = await openApp("/modules", "olga.lead");
    expect(await screen.findByRole("button", { name: /Стажёры/ })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Сбои/ }));
    expect(window.location.pathname).toBe("/incidents");
    expect(await screen.findByText(/incident-radar, по логам NGINX/)).toBeInTheDocument();
  });
});
