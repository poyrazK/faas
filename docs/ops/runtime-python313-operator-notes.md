# `python313` runtime: operator notes

Moved out of the customer page `docs/runtimes/python313.md` (ADR-20261028200304).

## Original introduction

Python 3.13 on the function surface (prewarmed persistent adapter with a
legacy per-request subprocess fallback, §4.9
envelope contract). Built by Railpack v0.31.1 with `--plan python`;
the underlying Python version is bound by the versioned `python-3.13` Wolfi
package in `images/runner-python313.Dockerfile`.
No new dispatch logic — Railpack's `--plan python` is version-agnostic,
so detection priority (`docker > node > python > go` in
`pkg/builderd/detect.go`) treats `python312` and `python313`
identically.

`python313` is **additive on top of `python312`**: existing
`python312` apps are unaffected. There is no default-flip in this PR —
`python312` remains the default for new Python functions.


## Operational configuration

The runtime base is **auto-staged** by `imaged` through
`pkg/imaged/base_stage.go::EnsureRuntimeBase`. The deployment pipeline
must write `FAAS_DEPLOY_BASE_REF_PYTHON313` as an immutable OCI digest in
`/etc/faas/runtime-bases.env`; the default `:latest` is for unnamed
development daemons only.

The production workflow is:
1. Publish the `images/runner-python313.Dockerfile` image to
   `ghcr.io/onebox-faas/runner-python313` and record its config digest.
2. Let the deployment pipeline render that digest into
   `FAAS_DEPLOY_BASE_REF_PYTHON313`.
3. Start or restart `imaged`. It pulls, validates, and stages the ext4
   automatically; subsequent boots short-circuit on the digest sidecar.

Operators must not build, copy, or manually place a runtime `.ext4` on a
compute node. A missing or invalid base is a deployment error, not a reason
to fall back to a hand-staged artifact.

If a non-digest-pinned `FAAS_DEPLOY_BASE_REF_PYTHON313` is set
(e.g. `:latest`), imaged aborts startup loud with a one-line error
naming the offending env var. The retired global `FAAS_DEPLOY_BASE_REF` is
rejected.

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

<!-- CI status: runtime migration, handler matrix, OCI auto-staging, and
runtime-image smoke coverage are implemented and enforced by CI. -->
