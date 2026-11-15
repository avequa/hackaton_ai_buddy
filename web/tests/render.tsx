import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import App from "../src/App";
import { TOKEN_KEY } from "../src/lib";
import { RouterProvider } from "../src/router";

/** Открыть приложение по адресу; если указан пользователь - уже вошедшим */
export async function openApp(path: string, userId?: string) {
  if (userId) localStorage.setItem(TOKEN_KEY, `t:${userId}`);
  window.history.replaceState(null, "", path);
  const user = userEvent.setup();
  const view = render(
    <RouterProvider>
      <App />
    </RouterProvider>,
  );
  return { user, view, screen };
}
