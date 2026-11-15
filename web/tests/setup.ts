import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.useRealTimers();
});

// jsdom не умеет scroll
window.scrollTo = () => {};
Element.prototype.scrollIntoView = () => {};
