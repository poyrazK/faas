# ADR-785: Explicit regional apps domain

Date: 2026-10-08
Status: Implemented locally; ADR-786 adds the durable publisher; regional serving acceptance pending.

## Context

ADR-784 consumes signed routes for every selected healthy eligible region. Its
publisher needs an app traffic address. The directory registry currently has
only a management API origin. Guessing a traffic address from that origin would
couple independent management and traffic endpoints and bypass verification of
the regional apps configuration. Ownership proof alone is not serving evidence.

## Decision

Add optional `apps_domain` metadata to the directory's cell registry. It is a
canonical lowercase DNS suffix with at least two labels, no literal IP, scheme,
port, wildcard, trailing dot or credentials. Each label is at most 63 bytes;
the suffix is at most 212 bytes, leaving room for the maximum 40-byte regional
app slug and separator inside a 253-byte DNS host. Limits live in
`pkg/api/limits.go`; the SQL constraint enforces the same spelling and bounds.
No address is inferred from `api_base`. Unconfigured cells remain usable for
legacy ownership attachment but provide no global traffic address.

An `apps_domain` change or explicit clear increments the registry generation,
together with management-origin or enabled changes, once per update. Existing
attachments become stale and require verification again. An unchanged retry
retains its generation. Operator API omission or null preserves the registered
suffix atomically, including concurrent older clients. An explicit empty string
clears it. `gregalectl cells set --apps-domain` carries this distinction through
the existing operator session, MFA and idempotency path. SDK models are generated
from the updated OpenAPI contract. The existing ADR-772/774/775 consent and
quota-delivery routes are also synchronized with OpenAPI. Generated transports
refuse redirects for these original-authority operations; Python request reprs
hide request-only regional keys. The Go SDK covers direct receipt transport
without importing native-owner packages or changing the signed wire spelling. The existing directory scope and immutable
cell identity guards continue to apply.

For a configured suffix, attachment verification requests
`GET /v1/cell/apps/{slug}/binding?traffic_origin=1`. After the existing regional
owner/admin authorization, the regional API returns `app_origin` derived from
its effective apps-domain configuration and the app's verified slug. The
directory requires the exact `https://<regional-slug>.<registered-apps-domain>`
origin. Missing capability or mismatched regional configuration fails the probe.
Unconfigured or invalid regional configuration returns 503 for a requested
proof. Unrecognized or repeated proof versions return 400.

Omitting the query preserves the old proof's JSON shape, allowing old directory
binaries with strict decoders to keep verifying ownership. Configure the suffix
only after regional binaries support the negotiated proof. Registry generation
is captured before the probe and checked again when attaching, so a concurrent
operator change cannot attach a proof against a different registry generation.

The binding and target structures retained in sealed delegations, consents and
quota grants do not change. A suffix change does not rewrite or resign those
original identities, release outstanding quota holds, revoke original consent,
or enable runtime admission. Existing delegation retirement on registry changes
and generation checks still apply. Rollback refuses a nonempty registered suffix;
operators must clear it explicitly first, which fences attachments. Rollback
retains the accumulated generations and the earlier registry guards.

## Remaining serving work

This is configuration evidence, not a readiness, health, native authority or
billing claim. APID-owned publication must combine current selection, generation,
original live consent, issued quota, regional admission and billing eligibility.
Regional admission remains frozen; receipt acceptance cannot activate a route.
Shared authorization/billing, host/session behavior, trusted client identity,
regional infrastructure/stores/replication, complete native inventory/ownership,
first-fence capacity, snapshots and immutable-input latency qualification remain
required, followed by dedicated native Linux x86_64 KVM metal, leakcheck and
fleet recovery acceptance. No live serving configuration is deployed here.

## Validation

Tests cover canonical suffixes and full-host boundaries; memory and real
PostgreSQL generation/preservation/clear contracts; concurrent legacy writers;
unchanged original quota/consent records and held capacity; stale attachment
rejection; SQL constraints and guarded rollback; negotiated and legacy proof
formats; exact origin mismatch rejection; authenticated operator and regional
API/SDK attachment flows; and CLI omission, set and clear requests.

Repository-wide compilation also exposed missing native-inventory and regional
retirement RPC methods in the existing gateway test client. The test client
returns `Unimplemented` explicitly for both; it grants no native authority.
Lint cleanup removes a private preview wrapper whose callers already use the
regional preview function, and annotates Linux-only supervisor state following
the file's existing platform-specific convention. Neither changes runtime behavior.
