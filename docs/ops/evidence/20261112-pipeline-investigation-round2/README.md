# Pipeline investigation, second pass — 2026-11-12 UTC

This pass adds PIPE-011–016 to the existing
[pipeline backlog](../../../pipeline-findings.md). It preserves the first ten
findings and all preexisting implementation/evidence files. No fixes are applied.

Seven probes cover six findings. **PASS means the observed problem or gap was
reproduced**, not that the pipeline is correct. These sources live outside normal
test discovery; convert/invert them into regression tests when fixing the issues.

| Probe | Finding | Evidence boundary |
| --- | --- | --- |
| `TestInvestigationRound2DisconnectSkipsApprovedReaderRevocation` | PIPE-011 | Real store, engine, deletion acknowledgement and restricted-role RLS SELECT; native resources already absent |
| `TestInvestigationRound2QueuedVerificationCannotBeCancelledWithoutSource` | PIPE-012 | Real catalog and engine unavailable-key path; no native scan |
| `TestInvestigationRound2VerifyAcceptedAfterPauseRequest` | PIPE-012 | Real pause/verify controls and a simulated ordinary paused worker observation |
| `TestInvestigationRound2LateHookInstallationNeedsRuntimeRestart` | PIPE-013 | Actual Node loader/gateway and PostgreSQL hook privileges; fake HTTP upstream |
| `TestInvestigationRound2VerificationDiscardsPrimaryKeyOrder` | PIPE-014 | Real PostgreSQL inspection and analyzed query plans on 100,000 synthetic rows |
| `TestInvestigationRound2CLIRejectsEquivalentConnectionUUID` | PIPE-015 | Actual CLI with a local fake API |
| `TestInvestigationRound2InitialCopyHasNoStallAlert` | PIPE-016 | Actual health/incident functions over modeled old copy state; no prolonged native-copy test |

Run from the repository root with its pinned Go toolchain, Python 3, Node 22 or
newer, and the runtime's pinned dependencies installed. Point the tests only at a
**disposable local PostgreSQL 16** with database/role creation permission and
local trust authentication (the Node probe creates a passwordless test login):

```sh
npm ci --prefix cmd/gregale/templates/data-api
export DATABASE_URL='postgres://faas@127.0.0.1:55432/faas?sslmode=disable'
export FAAS_PGTEST_TEMPLATE_DATABASE=1
export GO_BINARY=go
python3 docs/ops/evidence/20261112-pipeline-investigation-round2/run-probes.py
```

`GO_BINARY` may name the absolute path to the pinned Go executable. The runner
uses a temporary Go overlay for [domain_probes.go.txt](domain_probes.go.txt) and
[cli_probes.go.txt](cli_probes.go.txt). Remote source/mirror fixtures use isolated
disposable databases; catalog fixtures use the repository's isolated-schema
helper. Fixture roles and databases are removed by test cleanup. The actual Node
probe is [late-hook-probe.mjs](late-hook-probe.mjs). No customer database, provider
resource, external webhook or production account is used.

[probe-output.log](probe-output.log) records seven successful reproductions with
Go 1.26.10, PostgreSQL 16, and Node 24.19.0. The verifier query-plan comparison
observed about 50 ms with a full source scan/sort and 17 ms using the existing
primary-key order. Timing is diagnostic; assertions check the plan shapes, not a
speed ratio. The control query still has small bounded bookkeeping sorts.

No full native/provider acceptance or production rollout was performed during
this investigation. Each backlog entry includes the additional end-to-end
acceptance required before a fix can be considered complete.
