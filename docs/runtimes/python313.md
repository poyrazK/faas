# `python313` runtime

Python 3.13 on the function surface (prewarmed persistent adapter with a
legacy per-request subprocess fallback, §4.9
envelope contract). Built by Railpack v0.31.1 with `--plan python`;
the underlying Python version is bound by the OCI base image
(`python:3.13-slim-bookworm` from `images/runner-python313.Dockerfile`).
No new dispatch logic — Railpack's `--plan python` is version-agnostic,
so detection priority (`docker > node > python > go` in
`pkg/builderd/detect.go`) treats `python312` and `python313`
identically.

`python313` is **additive on top of `python312`**: existing
`python312` apps are unaffected. There is no default-flip in this PR —
`python312` remains the default for new Python functions.

## Function contract

The customer's source is a `handler.py` exporting a callable. Generated
adapters are prewarmed during runner startup and process newline-framed §4.9
envelopes in one long-lived subprocess; legacy protocol handlers retain one
subprocess per request. The runner reads the envelope from stdin and the
handler writes the response envelope to stdout. This is identical to the
`python312` contract; the only
differences are the runtime id (`python313` vs `python312`).

The handler filename **stays version-neutral**: `/app/handler.py` for
both `python312` and `python313`. The version is bound by the OCI
base image, not by the handler filename, so customers can move
existing `python312` handlers to `python313` by setting the runtime
field without renaming files.

The runner shim sets `FAAS_RUNTIME=python313` in the handler's
environment so customers can branch on runtime if they want.

### Minimal handler

```python
# /app/handler.py
import json, base64

def handler(request):
    body = base64.b64encode(json.dumps({"hello": "world"}).encode()).decode()
    return {
        "status": 200,
        "headers": {"content-type": "application/json"},
        "body_b64": body,
    }
```

### Local smoke test

The §4.9 envelope round-trips with bash and `base64` — the runner
starts `python3 /app/handler.py` and pipes newline-framed envelope JSON over
stdin:

```
echo '{"method":"GET","path":"/hello","headers":{},"query":"","body_b64":""}' \
  | python3 /app/handler.py
```

prints a JSON response envelope on stdout. The platform runs the same
script in production; nothing else differs.

### CGO

CGO is not applicable to Python runtimes.

## App contract

`python313` is a function-only runtime in this PR — there is no App
deployment path. Customers wanting a Python HTTP server use `type: app`
and `image: registry.gregale.dev/<digest>` to deploy a regular OCI
image (no runner shim).

## Base image

- Base ref: `ghcr.io/onebox-faas/runner-python313:latest`
- Source: `images/runner-python313.Dockerfile`
  (`FROM python:3.13-slim-bookworm@sha256:REPLACE_ME_AT_MERGE_TIME`)
- Disk: ~140 MB uncompressed, amortized across all `python313` apps
  via the two-drive scheme (drive0 = shared base, drive1 = per-app
  layer). Per-app cost is just the customer's `site-packages` + handler.

### Operator staging

In Tier 1 PR 2, the runtime base is **auto-staged** by `imaged` at
boot via `pkg/imaged/base_stage.go::EnsureBases`. Set
`FAAS_DEPLOY_BASE_REF_PYTHON313=<digest>` in `sealed.env` to
digest-pin the prod base; the default `:latest` is for dev only.

The pre-PR-2 staging recipe remains valid for boxes that haven't
upgraded imaged yet — see `images/runner-python313.Dockerfile` comments
for `docker build` + `mkfs.ext4` argv.

After PR 2 the operator workflow collapses to:
1. Publish the `images/runner-python313.Dockerfile` image to
   `ghcr.io/onebox-faas/runner-python313:<digest>`.
2. Set `FAAS_DEPLOY_BASE_REF_PYTHON313` to that digest in `sealed.env`.
3. Restart imaged. The first boot pulls + stages the ext4; subsequent
   boots short-circuit on the digest sidecar (Skipped=true).

If a non-digest-pinned `FAAS_DEPLOY_BASE_REF_PYTHON313` is set
(e.g. `:latest`), imaged aborts startup loud with a one-line error
naming the offending env var — the same posture as
`FAAS_DEPLOY_BASE_REF` (deploy-time override).

## Detection priority

`pkg/builderd/detect.go` priority order is
`docker > node > python > go`. The `python` branch matches on
`requirements.txt` (or `pyproject.toml`); it does NOT branch on
`requires-python` field, so a tarball with `requires-python: ">=3.13"`
still builds against the `python312` or `python313` base depending on
which runtime the customer chose elsewhere. Version selection is
operator-controlled via `FAAS_DEPLOY_BASE_REF_PYTHON313`.

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `unsupported function runtime "python313"` | migration `00075` not applied, OR imaged built before PR 1 merge | `goose up`; rebuild + restart imaged |
| runner exec fails with `ModuleNotFoundError: <pkg>` | customer handler uses a dep not vendored into the tarball | pin via Railpack `requirements.txt` lockfile, redeploy |
| `exec format error` on first wake | arm64 base deployed on amd64 host (or vice versa) | set `FAAS_DEPLOY_BASE_REF_PYTHON313` to the matching arch digest |

## See also

- `docs/STATUS.md` — runtime roster
- `pkg/api/build.go::FrameworkRailpackPython` — wire contract
- `pkg/builderd/dispatch.go::MapFramework` — detection → wire
- `guest/runners/python313/main.go` — function runner shim
- `guest/runners/python313/main_test.go::TestPython313RunnerHandlerDefault` —
  pins `/app/handler.py` against imaged argv
- `images/runner-python313.Dockerfile` — base image
- `migrations/00075_app_runtime_node24_python313.sql` — runtime enum widening

<!-- CI status: PR 1 (Tier 1) — migration 00075 applied; imaged runtime
matrix extended in pkg/imaged/base.go + handler.go. Base auto-stage
follows in PR 2. -->
