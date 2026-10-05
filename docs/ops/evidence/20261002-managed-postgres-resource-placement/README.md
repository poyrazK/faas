# Managed PostgreSQL resource placement diagnostics

This change journals jail directories and named network namespace bindings, checks
observed replacements before live-owner cleanup, and preserves prepared alias
identity. Reopened records still quarantine capacity. They do not grant cleanup,
process adoption, serving recovery or recovered-report authority to a new Manager.
See [ADR-401](../../../adr/401-managed-postgres-resource-placement.md).
Customer cutover remains disabled; supported native lifecycle acceptance is pending.

## Final source and host

Validation snapshot: `95d13a584b9f45a0f510cf553679c9f15014c61f`

Tree: `f7ef837ceb871e1c62fb394063f2f0198cfc2d2e`

Parent: `3ff2ff249abd19685f85e0349b95c67f9728384c`

All 6,252 workspace Go/module files match the pinned snapshot. The final commit
introduces no Go/module changes relative to it. The uploaded archive contains
4,209 selected Go/module files; all matched after execution. Archive, runner,
patch and manifest hashes are in `validation.json`. `change-patch.json` preserves
the exact UTF-8 patch lines and checksum; joining its lines recreates the patch.

Host: `gregale-prod / us-east1-b / gregale-internal-test-1`, n2-highmem-2,
Intel Cascade Lake, x86_64 Linux KVM, Go 1.25.13 and Firecracker/jailer 1.7.0.
Current gcloud metadata confirms nested virtualization (`host-description.json`).
This is an internal-node diagnostic, not native acceptance or a release gate.
Kernel and immutable two-drive fixture checksums and filesystem checks are in
`run.log`. No customer workload or PostgreSQL data is used.

The run uses a dedicated transient unit with a 10 GiB memory cap, private tmpfs
for TMPDIR (4 GiB) and jail (1 GiB), and an owned disk source/cache stage. Shared
module/build caches are copied into private caches. Compilation uses one Go
worker, GOGC=50 and GOMEMLIMIT=2GiB. The shared acceptance lock is held throughout
checks, and vmmd/builderd/imaged/internal-gateway services must be inactive.
The canary registry's `native-metal` kind is a cleanup classification.

## Results

Final selected metal diagnostics passed **41 top-level tests, 98 including
subtests, with zero skips/failures**. fcvm completed in 16.239 s and vmmd in
1.136 s. All three exit files are zero. Changed-code Linux lint reports zero
issues. Three in-run `make leakcheck` calls passed while the lock was held;
no extra host-wide leak check runs after releasing it.

The new real-kernel cases passed prepared alias transfer (0.02 s), a foreign
nested jail mount (0.01 s), and namespace replacement/plain-marker rejection
(0.03 s). The surviving-guest reopen test now checks both jail directories and
the named nsfs binding in addition to process and image-bind provenance.

Bounded Linux race regressions passed fcvm (1.223 s) and vmmd (1.297 s). Full
macOS race suites passed fcvm (19.521 s) and vmmd (10.178 s). Local changed-code
lint reports zero issues. Generated-unit consistency, text encoding, shell
quoting, sealed-env scope, ADR uniqueness and changed core-test citations passed.
macOS LC_DYSYMTAB linker warnings are nonfatal and preserved.

The first pinned run (`76e31c547c4011866491d33bb87bffbfa350ea45`) passed 40 top-level/97 total
tests, Linux lint and three leak checks. Final review added the rule that absence
must be observed in the recorded creator context. The final run repeats all
selected checks with that regression. `initial-*` files retain the earlier run;
final results above refer exclusively to the final snapshot. Earlier output/exit
files were moved aside, and the second source was unpacked into a fresh directory.

## Evidence and scope

The completed unit is collected: LoadState=not-found, ActiveState=inactive and
SubState=dead. Explicit exit files and completed logs independently prove success.
The source manifest and all selected runtime Go/module files match after execution.
The complete result archive has matching remote/local SHA-256 before stage cleanup
(`download-verification.log`). Both transient units are inactive. Canary cleanup recorded state=completed
and removed the owned disk stage (`cleanup.log`), leaving other jobs, services
and shared caches intact.

`make test-metal` selects `./pkg/fcvm ./cmd/vmmd` with `-race -count=1`,
`-timeout=20m -v` and the explicit RUN_REGEX in `runner.sh`. Unselected tests and
the unfiltered native release suite are outside scope. Reopen tests retain the
original live owner for cleanup. They do not simulate a full daemon crash,
recovered serving or filesystem power loss. Journal fixtures use TMPDIR tmpfs.

These checks observe identities; they are not atomic compare-and-delete or a
complete ownership epoch. Veth provenance, crash-safe prepared alias handoff,
child TUN/loop/parent mounts, jail-local links/copies, snapshot publication,
verified restart cleanup and durable all-node drain proof remain pending.
No dependency, migration, quota, RPC or environment variable is introduced.

Committed logs normalize carriage returns and trailing whitespace/blank lines.
`RAW_SHA256SUMS` records original local bytes; `SHA256SUMS` covers committed files.
