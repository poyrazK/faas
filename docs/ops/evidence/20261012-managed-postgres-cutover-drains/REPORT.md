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

The local scheduler run used a Go overlay that excluded historical scheduler
tests to stay within the Mac's compiler memory limit. It compiled production
scheduler code and both new test files. Full repository CI remains the complete
test-corpus gate. These fixture observations are not physical KVM evidence.

Migration replay and full up/down walk pass against isolated PostgreSQL 16.
SQLC generation, protobuf versions, ADR uniqueness and documentation SQL checks
pass. Production lint (golangci-lint 2.14.0) and Linux amd64 builds of schedd
and vmmd pass. This change adds zero apidserver production lines; the
baseline conflict was resolved using main's independently merged size-gate fix.

Physical native x86_64 Linux KVM `test-metal` and `leakcheck` are still required.
Full writer membership coverage, route convergence, external SQL-session fencing,
fresh target verification and atomic binding publication remain separate cutover
requirements. See [ADR-993](../../../adr/993-managed-postgres-cutover-drain-receipts.md).
