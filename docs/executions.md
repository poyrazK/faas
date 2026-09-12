# Disposable isolated runs

Gregale Runs executes caller-supplied Node.js or Python source in a fresh
Firecracker microVM. The VM receives loopback-only networking, bounded CPU,
memory, scratch space, and output limits, then is destroyed before the result
becomes terminal. Runs never expose a customer-mounted disk or preserve guest
state between requests. A run may include a bounded multi-file source bundle;
the files are sealed with the request and materialized only in the guest's
ephemeral scratch filesystem.

## CLI

```sh
gregale run --runtime node22 --file handler.js --input '{"url":"https://example.invalid"}' --wait
gregale run --runtime node22 --dir . --entrypoint src/index.mjs --wait
gregale runs list --status running --json
gregale runs status <execution-id>
gregale runs cancel <execution-id>
```

`--input` accepts inline JSON, `@path.json`, or `-` for stdin. `--file` accepts
only a regular, non-symlink file. `--dir` walks regular files below the
directory (skipping `.git`), rejects symlinks and special files, and enforces a
256-file / 1 MiB source cap before upload. `--entrypoint` must be a normalized
relative path present in the bundle. Use `--json` for a machine-readable
receipt.

## API

The API is account-scoped and requires a Bearer API key:

* `POST /v1/executions` — admit a run and return a queued receipt.
* `GET /v1/executions` — list account-scoped receipts with `limit`, `offset`, and optional `status` filters.
* `GET /v1/executions/{id}` — read the current or terminal receipt.
* `DELETE /v1/executions/{id}` — request idempotent cancellation.

The v1 runtime set is `node22`, `node24`, `python312`, and `python313`.
`network.mode` is always `none`; dependency installation, secrets, environment
injection, and persistent volumes are intentionally not supported. Source
bundles contain regular file bytes only—there is no symlink, device, or host
path representation. Source and input are encrypted before durable admission
and are never returned by reads.

The control-plane and scheduler gates are explicit. Set
`FAAS_EXECUTION_API_ENABLED=1` on apid and `FAAS_EXECUTION_DISPATCH=1` on
schedd only after the host's restore/execute/destroy isolation checks pass.

## Production smoke

After a release, run the operator smoke with an eligible account token:

```sh
FAAS_TOKEN=... make disposable-execution-release-smoke
```

The check first reads `GET /v1/capabilities`. When `disposable-runs` is
disabled, it verifies that admission fails closed with the documented 501
problem. When enabled, it submits a bounded Node 24 run and polls its receipt
until stdout contains the smoke marker. Set `GREGALE_API_URL` for a non-default
origin and `FAAS_EXECUTION_SMOKE_TIMEOUT_SECONDS` to change the polling limit.
