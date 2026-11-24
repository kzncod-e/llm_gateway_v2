# Bruno collection

API collection for the LLM Gateway: every endpoint, with tests, plus end-to-end scenarios.

## Setup

1. Start the stack from the repository root: `docker compose up -d`.
2. Create `bruno/.env` from `bruno/.env.example`, using `DEMO_API_KEY`, `ADMIN_EMAIL` and `ADMIN_PASSWORD` from the repository `.env`.
3. In Bruno, choose **Open Collection** and select this `bruno/` folder, then select the **Local** environment.

## Use

- **Run the whole collection** (Runner) to test everything. Folders run in order: login first, cleanup and logout last.
- **Single requests** work on their own: the collection script logs in automatically when `sessionToken` is empty. Requests that use IDs created earlier in a scenario (`teamId`, `userId`, `newKey`, `providerId`, `modelId`, `policyId`, `rpmKey`, `budgetKey`, `requestId`) need that folder run in order.

## Variables

| Variable | Where | Meaning |
|---|---|---|
| `baseUrl`, `mockUrl` | environment | Gateway and mock provider on the host |
| `baseUrlNoCookie` | environment | Gateway via 127.0.0.1, so the cookie jar sends no session (401 test) |
| `mockInternalUrl` | environment | Mock provider as the gateway container sees it |
| `apiKey`, `adminEmail`, `adminPassword` | environment ← `bruno/.env` | Demo Team key and admin login |
| `rangeDays` | environment | Days covered by analytics and log requests (default 7) |
| `demoTeamId`, `openaiProviderId`, `anthropicProviderId`, `geminiProviderId`, `gpt4oModelId`, `smartPolicyId`, `fastPolicyId`, `fallbackDemoPolicyId` | environment | Seeded IDs of the local database. List requests refresh them at runtime; update them here if the database is recreated |
| `sessionToken` | runtime | Console session from Login or the auto-login; cleared by Logout |
| `ts`, `dateFrom`, `dateTo` | runtime | Set by the collection script before every request |
| `teamId`, `userId`, `userEmail`, `newKey`, `newKeyId`, `providerId`, `modelId`, `policyId`, `rpmTeamId`, `rpmKey`, `budgetTeamId`, `budgetKey`, `requestId` | runtime | Created by scenario requests |
- From the command line: `cd bruno && npx @usebruno/cli run --env Local`.

| Folder | What it covers |
|---|---|
| 00 Health & Ops | `/healthz`, `/readyz`, `/metrics`, mock provider |
| 01 Auth | Login, me, profile update, 401 cases |
| 02 Data Plane - Chat | Models, routing aliases, fallback, explicit provider, provider hint, SSE streaming, stream interruption, error cases |
| 03 Users | Create, list, update |
| 04 Teams, Members & Budget | Team CRUD, members, budget status and limits |
| 05 API Keys | Create, use, revoke, revoked-key 401 |
| 06 Providers, Models & Routing (read) | Catalog, circuit state, price history |
| 07 Scenario - Onboard a provider | Provider → model → price → policy → chat → delete |
| 08 Request Logs & Analytics | Logs, attempt detail, filters, summary, timeseries, breakdowns |
| 09 Reliability, Audit & Settings | Reliability windows, audit log, runtime settings |
| 10 Playground | Console playground chat |
| 11 Scenario - Rate limit | 429 with `Retry-After` |
| 12 Scenario - Budget exhausted | 402 `budget_exceeded` |
| 13 Scenario - Provider outage | Mock failure injection, fallback, circuit state, breaker reset |
| 99 Cleanup & Logout | Archives test teams, disables the test user, logs out |

The run creates teams, a user, a provider and a policy with a `bruno-<timestamp>` suffix. Teams are archived rather than deleted because the API has no team delete.
