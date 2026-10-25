# ADR-20261025090000: Durable request promotion outcome recovery

## Status
Accepted.

## Context

A request deployment's apply and promotion are separate operations. The native
traffic PATCH uses a serving compare-and-swap, but a lost reply cannot be proven
from today's traffic: another release or rollback may already have changed it.
An expired bridge plan must not be reconstructed to find that historical result.

## Decision

Add an optional opaque `Deployment-Promotion-Attempt-ID` to guarded 100% manual
traffic promotion. APID validates its shape, the exact request, and app ownership.
MemStore and PgStore commit an immutable receipt with the existing traffic write
under the same lock/transaction. It binds the attempt and canonical request hash
to the candidate, previous serving deployment, scope and immutable named target.
Failed guards and failed transactions create no receipt. An accepted attempt
cannot execute a second traffic change, including after subsequent traffic moves.
Receipt lookup requires current deployment-read access and retained app/candidate
and named environment identities. Missing evidence never establishes rejection.

The bridge persists a separate encrypted, bounded promotion lookup alongside its
attempt marker before PATCH. It retains only opaque hashes/identity and candidate
metadata, not the mutation body or source/configuration values. The namespace is
preserved by older readers, pruned with action history and purged on grant
revocation. Previously attempted legacy plans never receive a new attempt ID.

`gregale_get_action_outcome` accepts optional `phase=apply|promotion` (apply by
default). Promotion recovery validates the original immutable target, receipt,
request hash, candidate and predecessor, then returns a redacted historical
receipt and an exact current deployment observation call. It works after plan
expiry without upgrading history, renewing authority, fetching approval, or
writing native state. History, prompts and review resources expose separate
apply and promotion outcome calls. Read consent and storage are checked again
before releasing the result.

## Consequences

Schema and API changes are additive. Receipts disappear when their app or
candidate is purged; absence remains unresolved. Receipt acceptance does not
prove current serving traffic, health, rollout completion or workload progress.
Native/provider execution qualification remains separate from portable fixtures.

## Validation

Table-driven Mem/Pg and HTTP tests cover atomic acceptance, refused guards,
request mismatch, concurrency, later traffic changes and read ownership. MCP
protocol tests cover independent phases, restart/expiry, encrypted references,
legacy plans, retention, N-1 preservation, and final consent/identity checks.
