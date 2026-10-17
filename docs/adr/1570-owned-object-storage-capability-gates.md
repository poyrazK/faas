# ADR-1570: Separate private object-storage capability gates

- Status: Accepted; private development only, no customer enrollment.
- Date: 2026-10-17
- Refines: [ADR-1569](1569-owned-object-storage-development-gate.md)

## Context

The master development flag currently permits both starting a private storage
node and loading its provider configuration. Operators need to exercise a node
without opting a gateway into it, and evaluate recovery settlements and logical
usage counters independently. Customer storage remains provider agnostic, with
no self-hosted selector in the UI, API or SDK.

## Decision

Keep `FAAS_OBJECT_NODE_ENABLED` as a required master flag. Add four independent,
default-off, startup-only operator flags:

- `FAAS_OBJECT_NODE_SERVER_ENABLED`: allow `object-storaged` startup. Require
  master and server before opening its data directory or reading credentials.
- `FAAS_OBJECT_NODE_BACKENDS_ENABLED`: allow shared provider loading of any
  backend requesting owned-node receipts, settlements or counters. Require
  master and backend before constructing any provider or reading credentials.
- `FAAS_OBJECT_NODE_SETTLEMENTS_ENABLED`: additionally allow loading backends
  whose `owned_node_settlements` option enrolls new recovery settlement jobs.
- `FAAS_OBJECT_NODE_USAGE_COUNTERS_ENABLED`: additionally allow loading backends
  whose `owned_node_usage_counters` option selects private logical usage reads.

Every flag uses the master's existing explicit true values and fails closed on
unset, false or invalid input. Return a configuration error identifying the
missing gate. A flag does not activate a backend option, imply another flag, or
replace backend validation. Settlement and counter options still require owned
receipts. The server flag is independent of all backend integration flags.

The settlement gate controls configuration for new enrollment. Saved settlement
jobs can drain after disabling `owned_node_settlements`, while the master and
backend gates and receipt capability remain enabled. Disabling a required flag
while its option remains configured refuses startup; never silently downgrade
the recovery or accounting protocol. Node receipt tracking, recovery, write
admission, audits and durability invariants retain their existing behavior.

GCS and managed S3/R2 loading does not query these gates. The existing
`s3_enabled` runtime switch still separately controls the customer storage
surface. No new flag appears in customer capabilities, UI, API, SDK or ordinary
deployment defaults. No deployment or customer enrollment is enabled.

Qualification removes inherited private flags and credentials. It explicitly
sets only master and server for its disposable daemon children; follower/control
children and SSH clients receive no private flags. Programmatic registries
remain internal test/qualification fixtures as in ADR-1569. Offline operator
maintenance and recovery tools remain available with serving disabled.

## Validation

Exercise all combinations of the five flags for server startup and each optional
backend capability, plus invalid values and missing dependency diagnostics.
Verify rejection before credentials or data-directory creation, independent
managed-provider loading, child environment isolation and parent preservation.
Rebuild binaries for default and format-5 qualification in both durability modes.
