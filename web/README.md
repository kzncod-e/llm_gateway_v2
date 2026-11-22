# LLM Gateway Console

Next.js 16 management console for the LLM Gateway. See the repository [README](../README.md) and [Development Guide](../docs/DEVELOPMENT_GUIDE.md).

```sh
pnpm install
BACKEND_URL=http://localhost:8080 pnpm dev     # http://localhost:3000
pnpm lint && pnpm typecheck && pnpm test       # static checks and unit tests
pnpm build && E2E_PASSWORD=… pnpm test:e2e     # browser end-to-end (needs the gateway)
```
