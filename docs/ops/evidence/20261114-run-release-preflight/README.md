# Run release tooling verification — 2026-11-14

**Result: release-tooling checks passed. Production native qualification remains
open.** See the [sanitized report](report.json) and [worktree check](worktree-check.json).

The tested source is qualified base snapshot
`432e6058ab21b335c2aea7bd633fe8e8df06bb68` plus the release-tooling delta with
SHA-256 `44bf92c967f7d093a5b6499efd5f934900e9fc917eb6862b187d30e91ad026df`.
The report binds all 14 source files and the built vmmd binary. The worktree
check confirms the other 50,866 non-documentation files match that base.
This is operational tooling verification, not another native VM lifecycle
qualification or a production rollback rehearsal.

On `gregale-internal-test-1` in `gregale-prod`, Go 1.26.11 Linux/amd64:

| Check | Result |
| --- | --- |
| Release package with race instrumentation | 63 passing test results; no failures or skips |
| vmmd CLI/configuration regressions with race instrumentation | 186 passing test results; no failures or skips |
| Production vmmd build | Passed with CGO disabled |
| Linux changed-source lint | All three packages passed: `pkg/runrelease`, `cmd/vmmd`, `pkg/api` |
| `capabilities` and `prepare` | Produced the real binary declaration and hash-pinned draft plan without invoking a referenced rollback binary |
| `preflight` | Expected exit 3, blocked specifically on host and native acceptance; Google virtualization and functional-only evidence cannot enable production |
| `rollback` | Exit 0 for a compatible binary with runtime capture disabled and retained roots; production enablement was not checked or granted |

The CLI fixture uses the same compatible binary as the proposed rollback
target, private configuration/artifact directories and the existing real
kernel, Node 24 base, guest-init, Firecracker, jailer and jail-helper files.
It installs nothing, starts no VM and changes no production service. The
check does not demonstrate rolling an older release through live traffic.
Existing drain, operation-completion, signed installation, base-adoption,
physical cleanup and usage checks remain required during an actual rollout.

Regressions cover missing/foreign/future reader declarations, changed pins,
disabled ownership, changed retained roots, capture still enabled on rollback,
mixed adoption, prepared networks, unsupported hosts, every missing lifecycle
scenario, wrong release-artifact evidence, symlinks, writable files, FIFO
rejection, bounded JSON, large streamed rootfs artifacts and cancellation.
An absent journal cannot authorize an older reader. Runtime admission is
never granted by either result.

The first Linux lint attempt returned exit 1 because its clean environment
lacked a lint-cache location. The retry explicitly uses the existing shared
`GOLANGCI_LINT_CACHE`; source and passing tests were retained. Both attempts'
raw logs remain on the node. Independent on-node verification rechecked source
and binary hashes, package/test completion, required test roots, lint exits
and both CLI results. Only fixed metadata, counts and hashes were exported.

Portable race checks and lint also passed locally. No SQL, RPC or native
journal format changes belong to this increment. The Run acceptance exporter
still cannot mint production eligibility; supported-native qualification,
approved runtime provenance and the existing runtime admission gates remain
required before capture enablement.

See the [operator runbook](../../run-release-preflight.md) and
[decision record](../../../adr/20261114020000-run-release-preflight-rollback.md).
