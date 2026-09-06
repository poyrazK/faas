# SDKs

Three first-class SDKs are built from this repo. **None of them is published
to a public registry yet** — see [publishing](../../sdk/README-publishing.md)
for the current install paths and what is still blocking release.

| Language | Install name            | Import as        | Source                    | Generator                          |
|----------|-------------------------|------------------|---------------------------|------------------------------------|
| Go       | `github.com/poyrazK/faas/sdk/go` | `faas`  | `sdk/go/internal/`        | hand-written (extracted from `pkg/api/` in PR 2) |
| Node     | `@gregale/sdk-node`     | `@gregale/sdk-node` | `sdk/node/src/generated/` | `openapi-typescript-codegen@0.72.1` |
| Python   | `gregale-sdk`           | `faas_sdk`       | `sdk/python/faas_sdk/`    | `openapi-python-client==0.29.0`     |

The Python install name and import name differ on purpose (`pip install
gregale-sdk` → `import faas_sdk`), because `faas-sdk` on PyPI is an unrelated
package owned by Sonra Intelligence Ltd.

All three consume the same canonical OpenAPI contract the apid daemon serves
(`api/openapi.yaml`). Generated output is committed per ADR-013.

## Regen workflow

After editing `api/openapi.yaml`, run **both** of these before committing:

```sh
make sdk-gen        # regen Node + Python SDKs, assert clean diff
make spec-check     # sync the pkg/apid/openapi.yaml embed copy, lint, AST parity
```

The two checks cover disjoint file sets (`sdk/` vs `pkg/apid/openapi.yaml` +
`docs/denylist.md`), so the order doesn't matter — but a single failure on
either one blocks the PR.

| Command                    | What it does                                                              |
|----------------------------|---------------------------------------------------------------------------|
| `make sdk-gen`             | Regen Node + Python SDKs + assert clean diff (this PR)                    |
| `make sdk-gen-node`        | Regen only the Node SDK                                                   |
| `make sdk-gen-python`      | Regen only the Python SDK                                                 |
| `make sdk-gen-node-check`  | Node regen + assert clean diff                                            |
| `make sdk-gen-python-check`| Python regen + assert clean diff                                          |
| `make sdk-gen-node-twice`  | Node determinism tripwire (regen twice, no diff)                          |
| `make sdk-gen-python-twice`| Python determinism tripwire (regen twice, no diff)                        |
| `make sdk-check`           | Coverage: every OpenAPI route has a typed Go SDK method (`cmd/sdk-coverage`) |
| `make sdk-smoke-node`      | Build fakeapid + run Node smoke suite                                     |
| `make sdk-smoke-python`    | Build fakeapid + run Python smoke suite                                   |

CI runs `make sdk-gen` (path-filtered on
`api/openapi.yaml|sdk/**|Makefile|cmd/sdk-coverage/**`) in parallel with the
per-SDK `sdk-gen-node` and `sdk-gen-python` jobs. The per-SDK jobs are
unfiltered defense-in-depth: they catch regen drift outside the aggregator's
path filter (e.g. a `.github/workflows/ci.yml` Node-version bump that changes
generator output).

## Cross-language smoke fixture

`sdk/fakeapid/` is a Go stdlib-only binary that mimics apid's wire shape for
the 5 routes the SDKs exercise. All three SDKs spawn it as a subprocess from
their own test harness. See `sdk/fakeapid/README.md`.
