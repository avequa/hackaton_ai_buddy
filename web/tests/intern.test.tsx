import { act, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { installFakeBackend, makeRequest } from "./fakeBackend";
import { openApp } from "./render";

describe("вход", () => {
  it("список сотрудников, вход, выход", async () => {
    installFakeBackend();
    const { user } = await openApp("/requests");
    expect(await screen.findByText("Анна Смирнова")).toBeInTheDocument();
    expect(window.location.search).toContain("next=");
    await user.click(screen.getByText("Анна Смирнова"));
    // вернулись туда, куда шли до входа
    expect(await screen.findByRole("heading", { name: "Мои заявки" })).toBeInTheDocument();
    await user.click(screen.getByTitle("Выйти"));
    expect(await screen.findByText("Павел Орлов")).toBeInTheDocument();
    expect(window.location.pathname).toBe("/login");
  });

  it("ошибка загрузки и повтор", async () => {
    const { fetch } = installFakeBackend();
    fetch.mockImplementationOnce(async () => new Response(JSON.stringify({ error: "ядро недоступно" }), { status: 503 }));
    const { user } = await openApp("/login");
    expect(await screen.findByText("ядро недоступно")).toBeInTheDocument();
    await user.click(screen.getByText(/Повторить/));
    expect(await screen.findByText("Анна Смирнова")).toBeInTheDocument();
  });

  it("протухший токен возвращает на вход", async () => {
    installFakeBackend();
    await openApp("/modules", "ghost");
    expect(await screen.findByText("Анна Смирнова")).toBeInTheDocument();
  });
});

describe("стажер", () => {
  it("главная, стартовый набор одной кнопкой", async () => {
    const { note, state } = installFakeBackend();
    note("anna.intern", "starter_pack", "Стартовый набор доступов", "Подготовить заявки?");
    const { user } = await openApp("/", "anna.intern");
    expect(await screen.findByText(/Добр/)).toBeInTheDocument();
    expect(window.location.pathname).toBe("/modules");
    await user.click(screen.getByRole("button", { name: /Стартовый набор/ }));
    const dialog = await screen.findByRole("dialog", { name: "Стартовый набор доступов" });
    expect(await within(dialog).findByText("GitLab")).toBeInTheDocument();
    expect(within(dialog).getAllByText("Уже есть")).toHaveLength(1);
    expect(within(dialog).getByText("Уже запрошено")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: /Подтвердить все \(2\)/ }));
    expect(await within(dialog).findAllByText("Отправлено")).toHaveLength(2);
    expect(await screen.findByText("Отправлено заявок: 2 из 2")).toBeInTheDocument();
    expect(state.requests.every((r) => r.status === "approved")).toBe(true);
    await user.click(within(dialog).getByRole("button", { name: "Готово" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("стартовый набор: все уже есть, и ошибка", async () => {
    const { fetch } = installFakeBackend();
    const real = fetch.getMockImplementation()!;
    let reply: Response | null = null;
    fetch.mockImplementation(async (input, init) =>
      String(input).includes("starter-pack") && reply ? reply : real(input, init),
    );
    const { user } = await openApp("/modules", "anna.intern");
    reply = new Response(JSON.stringify({ items: [{ service: "vpn", project: "corp", level: "read", outcome: "already_granted" }], draft_ids: [] }), { status: 200 });
    await user.click(await screen.findByRole("button", { name: /Стартовый набор/ }));
    expect(await screen.findByText(/уже есть или запрошены/)).toBeInTheDocument();
    await user.click(screen.getByText("Закрыть", { selector: "button.secondary-button" }));
    reply = new Response(JSON.stringify({ error: "только для стажеров" }), { status: 403 });
    await user.click(screen.getByRole("button", { name: /Стартовый набор/ }));
    expect(await screen.findByText("только для стажеров")).toBeInTheDocument();
    await user.click(screen.getByLabelText("Закрыть"));
  });

  it("чат: задача -> черновик -> подтверждение", async () => {
    const { state } = installFakeBackend();
    const { user } = await openApp("/chat/access", "anna.intern");
    const input = await screen.findByPlaceholderText(/Опишите задачу/);
    await user.type(input, "Нужно поправить алерт в дашборде платежей{enter}");
    expect(await screen.findByText("Подготовил черновик ACC-1001.", { exact: false })).toBeInTheDocument();
    // Markdown: жирный и код, а не звездочки
    expect(screen.getByText("Grafana", { selector: "strong" })).toBeInTheDocument();
    expect(screen.getByText("write", { selector: "code" })).toBeInTheDocument();
    expect(screen.getByText(/создал черновик заявки на доступ/)).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: /Подтвердить и отправить/ }));
    await waitFor(() => expect(state.requests[0].status).toBe("in_review"));
    expect((await screen.findAllByText("На согласовании")).length).toBeGreaterThan(0);
    // подсказка-чип и новый диалог
    await user.click(screen.getByRole("button", { name: /Новый диалог/ }));
    expect(screen.queryByText(/Подготовил черновик/)).toBeNull();
  });

  it("чат: отмена черновика и ошибка помощника", async () => {
    const { state } = installFakeBackend();
    const { user } = await openApp("/chat/support", "anna.intern");
    await user.type(await screen.findByPlaceholderText(/Опишите задачу/), "алерт{enter}");
    await user.click(await screen.findByRole("button", { name: /^Отменить$/ }));
    await waitFor(() => expect(state.requests[0].status).toBe("cancelled"));
    state.chatFails = true;
    await user.type(screen.getByPlaceholderText(/Опишите задачу/), "привет{enter}");
    expect(await screen.findByText(/Помощник сейчас недоступен/)).toBeInTheDocument();
    // вкладки модулей
    await user.click(screen.getByRole("button", { name: /Оборудование/ }));
    expect(window.location.pathname).toBe("/chat/equipment");
  });

  it("заявки: список, фильтры, поиск, карточка, отмена", async () => {
    const { state } = installFakeBackend();
    state.requests.push(
      makeRequest({ user: "anna.intern", status: "in_review" }),
      makeRequest({ user: "anna.intern", status: "rejected", title: "Доступ к проду", resolution: "только у дежурных", policy: { verdict: "needs_mentor", reasons: ["не по роли"] } }),
      makeRequest({ user: "anna.intern", kind: "helpdesk", status: "new", title: "Не работает VPN", access: undefined, ticket: { service: "vpn", summary: "не подключается", steps_tried: ["перезагрузка"] } }),
      makeRequest({ user: "anna.intern", kind: "equipment", status: "ready", title: "Монитор", access: undefined, equipment: { action: "issue", item: "lg-27", model: "LG 27", reason: "второй" } }),
    );
    const { user } = await openApp("/requests", "anna.intern");
    expect(await screen.findByText("Доступ к проду")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Завершённые/ }));
    expect(screen.queryByText("Не работает VPN")).toBeNull();
    await user.click(screen.getByRole("button", { name: /Активные/ }));
    expect(screen.getByText("Не работает VPN")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^Все/ }));
    await user.type(screen.getByPlaceholderText("Поиск по заявкам"), "монитор");
    expect(screen.queryByText("Доступ к проду")).toBeNull();
    await user.clear(screen.getByPlaceholderText("Поиск по заявкам"));
    await user.type(screen.getByPlaceholderText("Поиск по заявкам"), "zzz");
    expect(screen.getByText("Ничего не найдено")).toBeInTheDocument();
    await user.clear(screen.getByPlaceholderText("Поиск по заявкам"));
    await user.click(screen.getByText("Доступ к проду"));
    expect(window.location.pathname).toBe("/requests/ACC-1002");
    expect(await screen.findByText("только у дежурных")).toBeInTheDocument();
    await user.click(screen.getByText("Не работает VPN"));
    expect(await screen.findByText("перезагрузка")).toBeInTheDocument();
    await user.click(screen.getByText("Доступ write к Grafana / Дашборды платежей"));
    await user.click(await screen.findByRole("button", { name: /Отменить заявку/ }));
    await waitFor(() => expect(state.requests[0].status).toBe("cancelled"));
    await user.click(screen.getByRole("button", { name: /Обновить/ }));
  });

  it("пустые заявки и несуществующая заявка", async () => {
    installFakeBackend();
    await openApp("/requests/ACC-404", "anna.intern");
    expect(await screen.findByText(/Заявок пока нет/)).toBeInTheDocument();
  });

  it("путь адаптации: отметить свой шаг", async () => {
    const { state } = installFakeBackend();
    const { user } = await openApp("/onboarding", "anna.intern");
    expect((await screen.findAllByText("Познакомиться с командой")).length).toBeGreaterThan(0);
    expect(screen.getByText("Неделя 2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Готово" }));
    await waitFor(() => expect(state.steps["anna.intern"][0].done).toBe(true));
  });

  it("мои доступы", async () => {
    const { state } = installFakeBackend();
    state.requests.push(makeRequest({ user: "anna.intern", status: "in_review" }));
    await openApp("/access", "anna.intern");
    expect(await screen.findByText("Выданные доступы")).toBeInTheDocument();
    expect(await screen.findByText("Доступ write к Grafana / Дашборды платежей")).toBeInTheDocument();
  });

  it("стажера не пускает на экраны наставника и SRE, неизвестный адрес - 404", async () => {
    installFakeBackend();
    await openApp("/approvals", "anna.intern");
    await waitFor(() => expect(window.location.pathname).toBe("/modules"));
    await openApp("/incidents", "anna.intern");
    await waitFor(() => expect(window.location.pathname).toBe("/modules"));
    await openApp("/nope", "anna.intern");
    expect((await screen.findAllByText("Страница не найдена")).length).toBeGreaterThan(0);
  });
});

describe("уведомления и сбои", () => {
  it("колокольчик, переход по уведомлению, баннер сбоя от радара, плашка базы", async () => {
    const { state, note } = installFakeBackend({
      incidents: [{ id: "INC-101", service: "vpn", title: "52% запросов отвечают ошибкой 5xx", since: "2026-11-15T10:00:00Z", by: "incident-radar", message: "Чаще всего падает /api/v1/keepalive." }],
      degraded: true,
    });
    state.requests.push(makeRequest({ user: "anna.intern", status: "approved" }));
    note("anna.intern", "request", "ACC-1001: одобрено", "доступ выдан", "ACC-1001");
    note("anna.intern", "incident", "Сбой: Корпоративный VPN", "52% запросов", "INC-101");
    const { user } = await openApp("/modules", "anna.intern");
    expect(await screen.findByText(/обнаружено автоматически по логам/)).toBeInTheDocument();
    expect(await screen.findByText(/Сохраняем изменения/)).toBeInTheDocument();
    const bell = screen.getByRole("button", { name: "Уведомления" });
    await waitFor(() => expect(bell).toHaveTextContent("2"));
    await user.click(bell);
    const panel = screen.getByRole("dialog", { name: "Уведомления" });
    expect(within(panel).getByText("Сбой: Корпоративный VPN")).toBeInTheDocument();
    await waitFor(() => expect(state.notes.every((n) => n.read)).toBe(true));
    await user.click(within(panel).getByText("ACC-1001: одобрено"));
    expect(window.location.pathname).toBe("/requests/ACC-1001");
  });

  it("новое уведомление приходит опросом и показывается всплывашкой", async () => {
    const { note } = installFakeBackend();
    await openApp("/modules", "anna.intern");
    await screen.findByText(/Добр/);
    note("anna.intern", "incident", "Сбой: GitLab", "не работает CI", "INC-102");
    await act(async () => {
      await new Promise((r) => setTimeout(r, 4300));
    });
    expect(await screen.findByText("Сбой: GitLab: не работает CI")).toBeInTheDocument();
  }, 10_000);
});
