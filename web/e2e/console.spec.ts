import { expect, test, type Page } from "@playwright/test";

const email = process.env.E2E_EMAIL ?? "admin@example.com";
const password = process.env.E2E_PASSWORD ?? "";
const apiURL = process.env.E2E_API_URL ?? "http://localhost:8080";

test.skip(!password, "set E2E_PASSWORD (the bootstrap admin password) to run end-to-end tests");

async function login(page: Page) {
  await page.goto("/overview");
  await expect(page).toHaveURL(/\/login/);
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
}

async function pick(page: Page, label: string, option: string | RegExp) {
  await page.getByRole("combobox", { name: label }).click();
  await page.getByRole("option", { name: option }).click();
}

test("admin manages a team, issues a key, calls the gateway and inspects the logs", async ({ page, request }) => {
  const teamName = `E2E ${Date.now()}`;
  await login(page);

  // Overview renders real backend data.
  await expect(page.getByText("Total spend").first()).toBeVisible();
  await expect(page.getByRole("heading", { name: "Provider health" })).toBeVisible();

  // Create a team.
  await page.getByRole("link", { name: "Teams" }).click();
  await page.getByRole("button", { name: "New team" }).first().click();
  const teamDialog = page.getByRole("dialog");
  await teamDialog.getByLabel("Name").fill(teamName);
  await teamDialog.getByRole("button", { name: "Create team" }).click();
  await expect(page.getByRole("heading", { name: teamName })).toBeVisible();

  // Issue an API key; the raw key is shown exactly once.
  await page.getByRole("button", { name: "New key" }).click();
  const keyDialog = page.getByRole("dialog");
  await keyDialog.getByLabel("Name").fill("e2e-key");
  await keyDialog.getByRole("button", { name: "Create key" }).click();
  const key = (await keyDialog.getByTestId("created-key").textContent())?.trim() ?? "";
  expect(key).toMatch(/^gwk_[A-Za-z0-9_-]{43}$/);
  await keyDialog.getByRole("button", { name: "Done" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText(key)).toHaveCount(0);
  await expect(page.getByText(`${key.slice(0, 12)}…`)).toBeVisible();

  // The key works against the data plane.
  const res = await request.post(`${apiURL}/v1/chat/completions`, {
    headers: { Authorization: `Bearer ${key}` },
    data: { model: "fallback-demo", messages: [{ role: "user", content: "e2e ping" }] },
  });
  expect(res.status()).toBe(200);
  expect(res.headers()["x-gateway-provider"]).toBe("anthropic-mock");
  const body = await res.json();
  expect(body.usage.total_tokens).toBeGreaterThan(0);

  // Playground streams through the same engine for the new team.
  await page.getByRole("link", { name: "Playground" }).click();
  await pick(page, "Team", teamName);
  await pick(page, "Model", "smart");
  await page.getByLabel("Message").fill("Say hello from the e2e test");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByText(/\[mock (openai|anthropic)\//).last()).toBeVisible({ timeout: 20_000 });
  await expect(page.getByRole("link", { name: "View request log" })).toBeVisible();
  await expect(page.getByText("Total tokens").locator("..")).not.toContainText("—");

  // Both requests appear in the logs, and the fallback shows its attempts.
  await page.getByRole("link", { name: "Request Logs" }).click();
  await pick(page, "Team filter", teamName);
  const rows = page.getByRole("row");
  await expect(rows.filter({ hasText: "fallback-demo" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "smart" })).toHaveCount(1);
  await rows.filter({ hasText: "fallback-demo" }).click();
  await expect(page.getByText("Provider attempts")).toBeVisible();
  await expect(page.getByText(/Fallback from openai-mock to anthropic-mock/)).toBeVisible();
  await expect(page.getByText("#3")).toBeVisible();
});

test("unauthenticated visitors are redirected and API calls are rejected", async ({ page, request }) => {
  await page.goto("/teams");
  await expect(page).toHaveURL(/\/login\?next=%2Fteams/);
  const res = await request.get("/api/v1/teams");
  expect(res.status()).toBe(401);
});
