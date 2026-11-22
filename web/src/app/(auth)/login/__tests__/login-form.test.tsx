import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { LoginForm, safeNext } from "../login-form";

afterEach(() => vi.restoreAllMocks());

function renderForm() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <LoginForm />
    </QueryClientProvider>,
  );
}

describe("safeNext", () => {
  it("only allows same-site relative paths", () => {
    expect(safeNext("/teams/1")).toBe("/teams/1");
    expect(safeNext(null)).toBe("/overview");
    expect(safeNext("https://evil.example")).toBe("/overview");
    expect(safeNext("//evil.example")).toBe("/overview");
    expect(safeNext("/\\evil.example")).toBe("/overview");
  });
});

describe("LoginForm", () => {
  it("validates input before calling the API", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch");
    renderForm();
    await userEvent.type(screen.getByLabelText("Email"), "not-an-email");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Enter a valid email address")).toBeInTheDocument();
    expect(screen.getByText("Password is required")).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("shows the backend error message on bad credentials", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "invalid_credentials", message: "invalid email or password" } }), { status: 401 }),
    );
    renderForm();
    await userEvent.type(screen.getByLabelText("Email"), "admin@example.com");
    await userEvent.type(screen.getByLabelText("Password"), "wrong-password");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(screen.getByText("invalid email or password")).toBeInTheDocument());
  });
});
