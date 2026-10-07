# ADR-599: Customer Operations PostgreSQL transaction receipts

## Status

Accepted — 2026-10-06. Production admission remains disabled.

## Context

An ordinary customer handler can commit business writes and die before Gregale
receives its result. The operation must retain uncertainty even though the
business database has committed. ADR-586 provides scoped, replayable receipts
for managed backend operations, but its reserved headers and result envelope do
not implicitly apply to Customer Operations.

## Decision

Add `transaction_receipt: postgres_v1` to the immutable Customer Operations
definition and YAML/TOML manifests. It supports ordinary HTTP handlers with
`reconcile_on_unknown` recovery. Native workflows, jobs, automatic safe-retry
policies, arbitrary external effects and managed named webhook effects are
outside this adapter.

Admission retains protocol version 1 in the durable request. Claim and dispatch
author `X-Gregale-Customer-Operation-Receipt-Version` and
`X-Gregale-Customer-Operation-Receipt-Binding` after reserved-header stripping.
The binding hashes the captured account, app, customer, scope, definition
revision, deployment and release. It remains identical across approved
generations. Synthetic transport reconstructs it from retained platform data;
wire values cannot amend it. Current invocation, attempt and ephemeral capability
are required at the SDK boundary but never retained in customer receipts.

Node, Go and synchronous/asynchronous Python helpers reuse ADR-586's
customer-owned `public.gregale_operation_inbox`, PostgreSQL transaction lock and
receipt engine. The owner explicitly installs and retains the existing schema.
The request digest has a distinct Customer Operations domain and includes the
immutable binding plus exact method, request target and body. Scope or input
conflicts fail before business code. Managed and customer receipts cannot replay
each other even with the same UUID and input.

The callback returns only its ordinary JSON business result and performs writes
only through the supplied transaction. The shared storage envelope contains the
result and an empty effects array; the adapter validates and extracts the exact
retained result bytes before returning the HTTP body. It never asks the platform
to implicitly unwrap a managed envelope or creates a managed exclusive operation.
Replay must reject receipts containing named effects. Existing managed helpers
retain their previous wire format, digest and behavior.

Customer COMMIT and Gregale completion are separate transactions. Lost or
uncertain COMMIT acknowledgments return the existing explicit unknown-commit
error; helpers never automatically repeat the callback. Following an evidenced
operator-approved recovery, the new handler request checks the scoped receipt
first. An existing receipt replays without business work; an absent receipt can
execute the approved callback. The platform still validates the typed result and
fences stale, revoked, cancelled or late completion. It cannot inspect a private
customer database or infer provider safety from an asserted receipt.

The normal operation completion path creates one logical delivery; webhook
attempts and retries remain independent of the business transaction and result.
Progress, file uploads and other platform/provider I/O cannot be made atomic with
this customer transaction. An application that performs such effects still needs
reconciliation evidence before authorizing recovery. A new logical operation ID
is new work; business-level uniqueness remains the application's responsibility.

## Validation and rollout

The SDK gate covers real PostgreSQL rollback, concurrency, lost COMMIT
acknowledgments, HTTP process death before and after COMMIT, scope/input/binding
conflicts and all nine Customer Operations writer/reader language pairs. Exact
result replay preserves wide numbers and Unicode. The Customer Operations gate
adds memory and PostgreSQL control-plane acceptance against a separate customer
database: kill after COMMIT, retain reconciliation, approve one generation,
replay without callback, reject old completion, accept the original result once,
and retry a failed webhook with one logical delivery and one business write.

This changes no platform SQL schema, adds no automatic retry and grants no
admission. Native execution and operational qualification remain launch gates.
