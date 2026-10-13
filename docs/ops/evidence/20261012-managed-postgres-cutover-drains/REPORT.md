# Managed PostgreSQL cutover member drain receipts — 2026-10-12

This change adds internal per-instance retirement plumbing. Customer activation
and production provisioning remain disabled. No Neon resources or credentials
were used for this implementation's tests.

The new PostgreSQL integration, native-journal fixture, scheduler and gRPC tests
pass locally with Go 1.26.9. They cover atomic receipt/terminal-state/host-port
commit, late SQL failure rollback, missing cleanup, owner revision and ownership
ABA changes, changed wake/node/deployment, migration, cancellation locking,
lost replies, daemon restart after confirmed cleanup and delayed producers.
Receipts remain after terminal instance retention. Unsupported hosts and
unfinished crash cleanup with unknown helper ownership remain unconfirmed.

The initial local scheduler run used a Go overlay to bound compiler memory.
Subsequent runs compiled and passed the complete scheduler corpus without an
overlay using per-package `-c=1 -dwarf=false` flags and a 6 GiB Go memory limit.
The full managedpostgres, fcvm and vmmdgrpc suites also passed locally. These
fixture observations are not physical KVM evidence.

The first GitHub CI run found a missing gateway test-client RPC, a hand-typed
migration version and an unregistered drain table in the environment clone
inventory. The corrections add an explicit unsupported-RPC response, use the
migration generator and classify drain receipts as operational records that
cannot be copied into a cloned environment. CI on the corrected commit is the
complete repository gate.

Migration replay and full up/down walk pass against isolated PostgreSQL 16.
SQLC generation, protobuf versions, ADR uniqueness and documentation SQL checks
pass. Production lint (golangci-lint 2.14.0) and Linux amd64 builds of schedd
and vmmd pass. This change adds zero apidserver production lines; the
initial baseline conflict used main's independently merged size-gate fix.

A later CI merge included main's seven scan slug-holder wiring lines from
PR #4593 and three recovery-point fixture lines from PR #4595 without updated
size baselines. Those counters were synchronized during the CI investigation.
Main subsequently removed the size gate and its baseline files in PR #4637;
the final rebase preserves that removal. This PR adds no APID implementation
or internal tests.

Physical native x86_64 Linux KVM `test-metal` and `leakcheck` are still required.
Full writer membership coverage, route convergence, external SQL-session fencing,
fresh target verification and atomic binding publication remain separate cutover
requirements. See [ADR-993](../../../adr/993-managed-postgres-cutover-drain-receipts.md).
