# Pipeline investigation evidence — 2026-11-12 UTC

The durable backlog is [pipeline-findings.md](../../../pipeline-findings.md).
Product source files were left unchanged during this investigation.

These probes assert the current undesirable behavior. **PASS means reproduced.**
After fixing an item, adapt its probe into an ordinary regression test that
asserts the intended behavior; the investigation probe should then stop passing.

| Probe | Finding |
| --- | --- |
| `TestInvestigationNewReadOnlyLoginMissingHookGrants` | PIPE-001 |
| `TestInvestigationPauseAcknowledgement` | PIPE-002 |
| `TestInvestigationPausedMonitoringBlindSpot` | PIPE-003 |
| `TestInvestigationUnrelatedDiscoveryBlocksSetup` | PIPE-004 |
| `TestInvestigationWaitRejectsStaleFailure` | PIPE-005 |
| `TestInvestigationDigitLedMirrorName` | PIPE-006 |
| `TestInvestigationSourceNameCollision` | PIPE-007 |
| `TestInvestigationAuthPreflightAfterProvisioning` | PIPE-008 |
| `TestInvestigationLastReaderCannotBeRevoked` | PIPE-009 |
| `TestInvestigationResyncSkipsConfirmation` | PIPE-010 |

Run from the repository root with its pinned Go toolchain and Python 3. Use a
**disposable local PostgreSQL instance** with database/role creation permission:

```sh
export DATABASE_URL='postgres://faas@127.0.0.1:55432/faas?sslmode=disable'
export FAAS_PGTEST_TEMPLATE_DATABASE=1
export GO_BINARY=go
python3 docs/ops/evidence/20261112-pipeline-investigation/run-probes.py
```

`GO_BINARY` may instead name an absolute path to the appropriate Go executable.
The runner uses a temporary Go overlay to include
[domain_probes.go.txt](domain_probes.go.txt) and
[cli_probes.go.txt](cli_probes.go.txt). It does not add test files to product
package directories. The PostgreSQL test helpers create and drop isolated
databases/roles; do not point the command at a customer or production database.
Local CLI fake servers accept placeholder credentials only.

See [probe-output.log](probe-output.log) for the captured successful reproduction
run: all ten probes reproduced their findings using Go 1.26.10 and disposable
PostgreSQL 16. PIPE-001 reproduces actual PostgreSQL privileges and checks the related
runtime/worker source; it is not a full provider rotation test. PIPE-007 captures
the generated-name collision in the CLI and relies on the real catalog uniqueness
constraint for its conflict consequence. PIPE-008 uses simulated provider
responses. These limits also appear in the backlog.
