# ADR 1214: Native PostgreSQL COMMIT acknowledgement qualification

Date: 2026-10-12
Status: Accepted

## Context

Portable PostgreSQL tests already prove that a successful commit can lose its
acknowledgement. Native authentication qualification exercises deployment and TLS,
but does not establish correct write outcomes under protocol response loss.

## Decision

Add a fifteenth native acceptance case using the existing write caller, migrated
PostgreSQL ledger, and protocol proxy inside payment's loopback interface. Serving
requires an explicit disposable opt-in and sealed, short-lived TLS/control inputs.
The upstream connection verifies the external database's public CA and DNS name.
No guest port, firewall change, migration credential, or platform API is added.

Arm one successful COMMIT response drop after startup. Require a 503 unknown outcome
with one initial provider attempt, independent SQL proof of one confirmed matching
charge before replay, successful explicit same-key replay, rejection of changed
payloads, and unchanged row counts and ledger/provider identities. Capture positive
fault evidence and joined connections without SQL, payloads or credentials.

Reuse flat application assertions and cleanup, preserving staged scenario
validation. Cleanup stops the fixture and deletes the exact run namespace. An
independent host probe checks namespace removal and backend retirement even when
receipt validation fails. The dedicated database fixture must not admit unrelated
new sessions while the PID-baseline retirement checks run.

Raise the native verdict to version 7 and require all fifteen cases. Local real
PostgreSQL tests exercise the exact deployed sources and native proof validator,
but cannot replace designated native x86_64 KVM qualification.

## Consequences

The gate now verifies data correctness after an ambiguous network failure, beyond
availability. It requires the existing migrated runtime DML role and dedicated
external PostgreSQL fixture. KVM qualification remains blocked in this workspace.
