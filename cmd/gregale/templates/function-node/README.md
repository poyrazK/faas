# function-node

A minimal node22 function handler.

Functions differ from apps in two ways:

1. No HTTP server — the runner invokes `handler(event, ctx)` directly
   for each request.
2. CLI forces `--runtime node22 --handler handler.handler` so the
   wiring is automatic. You don't need to know those flags.

## Deploy

```
gregale deploy --template function-node --name <slug>
```

## Invoke

```
gregale open <slug>   # browser test page, or POST from any HTTP client
```
