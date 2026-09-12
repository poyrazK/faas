# Disposable isolated runs

Gregale Runs executes one caller-supplied Node.js or Python file in a fresh
Firecracker microVM. The VM receives loopback-only networking, bounded CPU,
memory, scratch space, and output limits, then is destroyed before the result
becomes terminal. Runs never expose a customer-mounted disk or preserve guest
state between requests.

## CLI

```sh
gregale run --runtime node22 --file handler.js --input '{"url":"https://example.invalid"}' --wait
gregale runs status <execution-id>
gregale runs cancel <execution-id>
```

`--input` accepts inline JSON, `@path.json`, or `-` for stdin. `--file` accepts
only a regular, non-symlink file. Use `--json` for a machine-readable receipt.

## API

The API is account-scoped and requires a Bearer API key:

* `POST /v1/executions` — admit a run and return a queued receipt.
* `GET /v1/executions/{id}` — read the current or terminal receipt.
* `DELETE /v1/executions/{id}` — request idempotent cancellation.

The v1 runtime set is `node22`, `node24`, `python312`, and `python313`.
`network.mode` is always `none`; dependency installation, secrets, environment
injection, multi-file bundles, and persistent volumes are intentionally not
supported. Source and input are encrypted before durable admission and are
never returned by reads.

The control-plane and scheduler gates are explicit. Set
`FAAS_EXECUTION_API_ENABLED=1` on apid and `FAAS_EXECUTION_DISPATCH=1` on
schedd only after the host's restore/execute/destroy isolation checks pass.
