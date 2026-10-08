# ADR-772: Directory quota delivery and regional receiver

Date: 2026-10-06
Status: Implementation and validation in progress; regional activation remains frozen.

## Context

ADR-741 provides directory signatures and ADR-742 provides the regional receipt
journal. Neither is wired into apid. A global allocation therefore cannot reach
its selected independent cells through the API. Delivery must preserve the one
global concurrency budget, immutable original identities and uncertain charges.

## Decision

Directory apid optionally loads a distinct directory-role Ed25519 key using the
ADR-760 operator files. Its issuer UUID is first bound to the unbound database
in a retained singleton. The database rejects replacement, removal, truncation,
regional rebinding and use of that UUID as a registered cell. Concurrent first
startup accepts one issuer. Rotation keeps the same UUID and overlapping public
anchors; it does not replace accepted envelopes. Partial configuration refuses
startup. No authority files are provisioned or deployed by this change.

The explicit authenticated directory operation is
`POST /v1/global-apps/{slug}/runtime-quotas/{allocation_id}/deliver/{region}`.
The body contains `regional_api_key`; it is request-only and required for a
grant. An issued marker commits before a signed grant leaves the directory.
Delivery always addresses the allocation's retained canonical HTTPS origin and
immutable regional app UUID. Redirects are refused. Transport errors and remote
bodies are discarded; uncertain delivery retains the charge and can be retried
under the same identity. Receipt evidence is checked against the complete sent
claims using operator-pinned overlapping directory keys.

Regional apid accepts grants at
`PUT /v1/cells/{cell_id}/apps/{app_id}/runtime-quotas/{allocation_id}`.
Existing bearer authentication, admin scope, MFA, verified email and personal
account/app authorization establish local consent. The receiver independently
compares its database-verified cell, configured origin, live app UUID/slug,
account, personal organization and app region. Bounded canonical transport and
signed claims must match the path **before** journal mutation. Acceptance uses
the existing state receiver; every resulting admission head stays frozen.
Personal binding proofs omit the organization UUID. The local journal accepts
that original blank scope only for a legacy null-org app or the original
account's active personal organization. It locks the actual app and tenant,
then rechecks tenant state and personal ownership after any lock wait. A blank
scope cannot authorize a shared organization. Existing explicit organization
scopes still require the exact original app tenant; receipts keep the original
signed binding rather than replacing its omitted field.
The capacity and serving-seat SQL guards apply the same app/tenant checks to
direct writes. Their additive migration preserves those checks on rollback,
along with every existing immutable identity, conservation and closure guard.

Retirement uses the same path with `/retirement`. Pinned directory authority
authenticates this narrowing operation; it needs no customer credential or live
app row. Its full original binding must still match this configured cell and
origin. This permits tombstones before delayed grants and after app/key
collection without creating an authentication replication dependency. Directory
retirement delivery sends no regional credential. It returns retained retirement
evidence, including historical entitlement policies. Operators retain original
origins and public keys while historical deliveries remain outstanding.

Responses contain original directory-signed envelopes. They prove journal
receipt storage only. A retirement response is **not** a cell-signed drain and
never releases directory capacity. There is no drain-claim or activation route.
These explicit operations are the retry path; a durable automatic delivery
sweeper remains required. Existing shared-organization runtime/billing gates
remain enforced; this first transport supports personally owned app grants.

Manual operator configuration (absolute paths, outside the repository):

```toml
# Unbound directory apid. Preserve this UUID across restarts and key rotation.
[runtime_quota_transport]
directory_issuer_id = "<immutable-directory-uuid>"
private_key_path = "/secure/directory/key.pem"
trust_bundle_path = "/secure/directory-overlap-trust.json"
```

```toml
# Regional apid. No directory private key is installed in a cell.
[runtime_quota_transport]
regional_api_base = "https://regional-api.example.com"
trust_bundle_path = "/secure/directory-overlap-trust.json"
```

No manifest, routing, infrastructure or production configuration is activated.
Serving from every selected healthy eligible region remains the product default.
Full implementation still requires complete native producer inventory and
original run/task authority, inventory reconciliation and capacity activation,
snapshot shape provenance and capture/pause/migration, global billing and shared
organization runtime consent, independent regional provisioning/stores and
replication, cached ingress publication and automatic recovery/delivery. Native
Linux x86_64 KVM metal/leakcheck/recovery acceptance remains mandatory. Internal
GCE nested-node diagnostics cannot satisfy that gate.

## Validation

Exercise real HTTPS delivery to two independently authenticated regional apids,
commit-before-lost-response retries, exact retained envelopes, frozen shares,
retirement after deletion, no credential forwarding on retirement and retained
directory charges. Reject wrong paths, bindings, signatures, missing admin
consent, duplicate/oversized JSON and redirects before journal mutation. Check
issuer races, reload/rotation and SQL immutability, both scope directions and
guarded migration downgrade using private PostgreSQL. Run affected race tests,
pinned lint, sqlc regeneration and repository policy gates.
