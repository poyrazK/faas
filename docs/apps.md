# Apps

An app is a named HTTP API with a current deployment, runtime profile, and
optional domains, secrets, environment variables, and triggers.

```bash
gregale apps
gregale app my-api
gregale app my-api scale --min 1
gregale open my-api
gregale apps --q my-api
```

`gregale deploy` creates the app on first use and updates it on later runs.
Use `gregale app my-api --json` in automation; API errors include a stable
code, the observed value, the limit, and a next action.

Apps are isolated from one another. A parked app consumes no resident memory;
its next request wakes a snapshot or cold-boots from its immutable artifact.
See [scale-to-zero](cold-wake.md) and [plans](plans.md) for the limits that
control this lifecycle.
