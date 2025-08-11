# AIB Web UI

Dev setup:

1. Install deps:

```
pnpm i
```

or

```
npm i
```

2. Run locally with API proxy to `http://localhost:8080`:

```
VITE_API_BASE=http://localhost:8080 pnpm dev
```

If not set, the dev server proxies `/v1` to `http://localhost:8080` by default.


