# function-node24

A minimal Node 24 LTS function handler.

Functions differ from apps in two ways:

1. No HTTP server — the runner invokes `handler(event, ctx)` directly
   for each request.
2. `gregale.yaml` records `runtime: node24` and `handler: handler.handler`,
   so both direct template deploys and a later plain `gregale deploy --path`
   keep the function shape. You don't need to know those flags. The
   underlying handler filename in the microVM is `/app/node24.js`
   (versioned, mirroring the `node22` convention).

## Deploy

```
gregale deploy --template function-node24 --name <slug>
```

## Invoke

```
gregale open <slug>   # browser test page, or POST from any HTTP client
```

## Differences from `function-node`

- Runtime is `node24` (Node 24 LTS) instead of `node22` (Node 22 LTS).
- `engines.node` advertises `>=24`. The OCI base image in
  `images/runner-node24.Dockerfile` is bound to a Node 24 digest at
  build time; the underlying Node version is operator-controlled via
  `FAAS_DEPLOY_BASE_REF_NODE24`.
- All other behavior is identical — same envelope contract (§4.9),
  same metric set, same handler shape.

See `docs/runtimes/node24.md` for the per-runtime contract.
