# ADR-1358: Keep self-hosted object storage behind a development gate

- Status: Accepted; private development only, no customer enrollment.
- Date: 2026-10-17
- Related: [ADR-1331](1331-owned-object-storage-first-node.md), [ADR-1341](1341-owned-object-storage-local-qualification.md)

## Context

The existing customer storage surface uses the operator runtime switch
`s3_enabled`, which defaults to false, and a configured provider registry. The
private `object-storaged` prototype is loopback-only and has no customer provider
selector or deployment unit. Its explicit startup and private backend options
previously lacked an independent feature gate. Enabling provider-backed storage
must not implicitly make the development engine available.

## Decision

Add the startup-only operator flag `FAAS_OBJECT_NODE_ENABLED`, default off.
Accept only `1`, `true`, `yes` or `on`, ignoring case and surrounding whitespace;
all other values keep the node disabled. The private daemon refuses startup
before opening or creating its data directory unless this flag is enabled.

The shared `objectstorage.Load` path used by production daemons rejects any
backend requesting owned-node receipts, settlements or usage counters unless the
same flag is explicitly enabled. Check before constructing providers or reading
provider credentials. Ordinary GCS and managed S3/R2 configuration is independent
of this gate. `s3_enabled` remains required for the public storage surface.

Keep the new gate out of customer capabilities, UI, public API, SDK and ordinary
deployment defaults. It grants no production enrollment, customer configuration,
public listener or routing change. The node remains loopback-only with private
operator credentials. Operators must keep this flag unset on customer-serving
installations.

The explicitly invoked qualification harness enables the gate only in its
isolated child environments, replacing any inherited value. Programmatically
constructed registries remain available to internal tests and disposable
qualification fixtures. This does not change the parent environment or a live
installation. Existing offline maintenance/recovery tools remain available to
operators when the serving daemon is disabled.

## Validation

Cover default-off and invalid values, private backend refusal before credential
access, explicit development opt-in, managed-provider independence, disabled
startup without filesystem changes, and isolated child environment handling.
Rebuild the node and exercise default and format-5 process qualification in both
durability modes. No live deployment is enabled by this change.
