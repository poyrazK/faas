# Run system tools and Task SDK verification

The preview implementation adds exact Wolfi system packages, packages-only
environments, versioned build/image worker states, and typed TypeScript/Python
Task handles. The [PDF example](../../../../examples/gregale-run/pdf-report/README.md)
combines a stored PDF input, pinned Poppler, a result schema and an HTML output.
The [ADR](../../../adr/20261117183000-run-system-packages-and-task-sdk.md)
records compatibility and scope.

## Completed checks

| Check | Result |
| --- | --- |
| Full recipe and environment API domain tests | Passed |
| Memory and isolated PostgreSQL environment lifecycle and new worker states | Passed |
| Real migration application in the isolated PostgreSQL tests | Passed |
| Three new expand/validate/contract migration lint checks against `rc339` | Passed |
| Generated SQL query parity | Passed |
| Builder slot, immutable export and cleanup tests for both recipe formats | Passed |
| CLI forwards repeated system pins without language dependencies | Passed |
| HTTP extended-class writer rollback and diagnostic read rollback | Passed |
| OpenAPI AST compliance and test-store conventions | Passed |
| Focused Go vet for recipe, domain, builder, state and CLI | Passed |
| TypeScript build and focused Task/file/delivery/diagnostic SDK tests | 11 passed |
| Python sync/async Task and related SDK tests | 11 passed |
| Post-regeneration Python Task tests | 7 passed |
| SDK regeneration | Both completed; models/helpers retained |
| PDF HTML escaping and extraction limit tests | 2 passed |
| TypeScript PDF driver type-check | Passed |
| Functional verdict coverage and refusal tests | 20 passed |
| Final Linux metal cohort compilation with the race detector | Passed |
| Focused Linux lint with production CGO settings | Passed |
| Acceptance runner shell syntax and ShellCheck | Passed |

SDK recovery checks cover stable invocation keys, missing-file staging, replay
before upload, persisted intents, uncertain acknowledgements, invalid results,
observation timeouts without remote cancellation, and retained pending delivery
receipts. Invalid Node wait limits are refused before submission.

The full OpenAPI lint has 97 errors in the current snapshot and the same 97
errors with this field removed; this increment adds no error locations. A macOS
focused lint run reports the existing Linux-only `removeBuildKitSeeds` helper as
unused. The final Linux metal cohort compiles successfully with the race detector;
focused Linux lint passed with CGO disabled to match production builds. The first
state race compile was interrupted under shared-host memory pressure; the
acceptance runner now uses the repository's serial compiler settings for large
packages. No VM was started by that compilation check.

## Functional PDF qualification pending

An exact source snapshot was uploaded and verified in a separate directory on
the previously selected `gregale-internal-test-1` node in `gregale-prod`:

- Base commit: `e74c7cfc6e5b9cff5596d7f430a4d5e0c6bbfab1`.
- Source archive SHA-256: `d59371f13ad75c4c21778d4b1fa7da38fe57d46bd4fa2fd588ff70cdb3050911`.
- Source manifest SHA-256: `661fabb818f9136ee7d88a368fee40c242a3c89ff14ee57e18d6852c27bae508`.
- Source files: 55,902, including unpublished work.
- Node source: `/mnt/e2e-scratch/gregale-run-system-env-d59371f13ad7/source`.

The final delta has archive SHA-256
`6c513583124411b4021eb6066aeef6738eec0aa344e4717b87d18321bc54639f`
and source manifest SHA-256
`956ccc8117f64943056c364bffb0725e7e8f1e37e593ec9089d972aed25a9019`.
The applied source now includes 55,903 files, the final API limit diagnostics,
retirement assertion, example launch files and bounded compiler settings.
The [prepared command](commands/functional.sh) selects the exact source and pins.
The new `system-environments` cohort requires all four journeys, real isolated
package installation, a complete produced-image scan, verified stored HTML,
task replay, one usage entry, retirement and clean leak checks.

The node's available scratch space is below the existing 20 GiB builder guard.
No VM build or PDF journey has been reported as passed. The exact 1,498-file node cache eviction was separately approved under
`CLAUDE.md`; all 1,498 entries passed rechecks and were removed, freeing 8.00 GiB. Source verification confirms the final
manifest, and the requested ten-minute wait restored about 14 GiB of available memory.
After cleanup, 12.41 GiB of scratch remained, below the 20 GiB builder guard.
Scratch later recovered to 24 GiB as other temporary work was released. The
bounded Linux compilation and focused production lint passed. The guarded VM
journey is queued behind other acceptance runs. Functional results on this virtualized node
cannot establish native production runtime eligibility. Preview writers remain
gated; no deployment or activation was performed.

The queue-only runner delta has archive SHA-256
`128e2cbee3e819e9eac60bff81cb64042c93bf2932d1cd3a1221eabe873312c4`
and manifest SHA-256
`25922853215dd0bbf1aa81e7e844063a4a128f3aeda60313fa8960175c9acace`.
It retains the normal 30-second lock wait and allows an explicit bounded wait
of up to two hours for a busy acceptance host. It preserves mutual exclusion
and changes no Go implementation.

## Real VM attempts

The first attempt stopped at VMMD startup because the retained host ownership
journal requires the existing native recovery profile. Both leak checks passed.
The system cohort now uses that profile in the runner's private mount namespace;
it preserves the retained journal. The profile-only source delta has archive
SHA-256 `036f3402634736238568e382ab4aa4cde98429e4aef7f4d52dd57cab43540ad1`
and manifest SHA-256
`ef3f4c90c6925a5bca8200299261ecf93b28709f52047f6c52b30af4c19f2645`.

The second attempt started VMMD successfully and passed versioned recipe
admission and immutable reuse. The real builder refused to allocate its drive:
12,125,839,360 bytes were available against the unchanged 21,474,836,480-byte
minimum. The final leak check passed. Installation, scan, PDF execution and
retirement journeys remain unqualified. A separate exact 55-archive, 16.03 GiB
cache proposal is prepared; additional eviction awaits approval.
