# Global app directory and verified regional bindings

ADR-476 introduces a gated directory identity API. A global app has one UUID
and `all_selected_regions` as its serving policy. This slice attaches existing
regional apps and resolves their identities. It does not provision deployments
or publish ingress routes. Global deployment orchestration, shared org/auth,
runtime quota allocation, data/secret replication, and independent native KVM
recovery acceptance remain required for the complete multi-region feature.

## Configure the directory and regional cells

Provision a separate directory PostgreSQL database and `apid` endpoint using
the normal account authentication and operator session configuration. Keep the
directory database **unbound**: do not initialize `control_plane_cells` there.
Enable `global_app_directory_enabled = true` in its `apid.toml`, or set
`daemons.apid.global_app_directory_enabled: true` in its fleet manifest.
The default is false. Startup refuses a bound regional database. SQL guards
reject directory writes in regional databases and prevent a populated directory
database from becoming a regional cell. Unbound identity does not by itself
prove physical host independence; the operator provisions the separate database.

Each regional fleet uses ADR-475's immutable cell UUID and region. Follow
[regional cell initialization](regional-control-plane-cells.md) before exposing
the proof endpoint. Regional APIs keep their independent credentials and quotas.

Authenticate the operator CLI to the **directory API** with a fresh MFA step-up
session. Register each cell's exact UUID/region and HTTPS API origin:

```sh
gregalectl cells set --cell-id CELL_UUID --region eu-fsn \
  --api-base https://api.fsn.example.com --enabled=true
gregalectl cells set --cell-id OTHER_CELL_UUID --region eu-hel \
  --api-base https://api.hel.example.com --enabled=true
gregalectl cells list
```

The API origin has no path, trailing slash, credentials, query or fragment.
Cell UUID and region are immutable. An endpoint or enabled-policy change
increments the registry generation; an unchanged retry does not. Disabling
a registry entry blocks new attachments and resolution, without changing or
deleting regional workloads. Re-enabling requires ownership verification again.

## Register and attach a customer's app

Authenticate `gregale` to the directory API with the directory account's key.
Create the global identity, then prove ownership of each existing regional app
using its separate personal account-admin key:

```sh
gregale global-app create global-shop
gregale global-app attach global-shop --region eu-fsn \
  --regional-app shop-fsn --regional-token-env GREGALE_FSN_ACCOUNT_KEY
gregale global-app attach global-shop --region eu-hel \
  --regional-app shop-hel --regional-token-env GREGALE_HEL_ACCOUNT_KEY
gregale global-app show global-shop
gregale global-app resolve global-shop --region eu-fsn
```

Populate the named environment variables through your credential workflow;
do not put keys in command-line arguments. The global key authenticates only
the directory request. The regional key is sent only to the registered HTTPS
origin for `GET /v1/cell/apps/{slug}/binding`; redirects are refused. The Go, Node and Python SDK
attachment transports also refuse directory redirects. Neither the directory
nor its audit events retain the key. Proof responses are bounded
to 4 KiB with a maximum 10-second request timeout. Request bodies are bounded
to 4 KiB. Canonical slugs are 3–40 bytes; origins are at most 2048 bytes.
Bounds live in `pkg/api/limits.go`; directory registrations use the account
plan's existing `DeployedApps` count and create no runtime resource entitlement.

Legacy personal apps and apps owned by the caller's active personal organization
can be attached. Shared organization apps, non-personal org keys, read-only
keys, revoked keys, and scoped deploy tokens cannot establish personal ownership.
Matching email addresses or app names never establish cross-cell identity.
The first successful attachment fixes the directory account's regional account
mapping; other directory accounts cannot claim that regional account. A cell's
regional app UUID can be claimed by only one global app at a time.

Resolution returns an API origin plus regional app UUID and slug for an
**explicit region**. Authenticate calls to that API with regional credentials.
No global key is forwarded, and the resolver never selects an implicit primary.
All selected, healthy and eligible regions will serve when global ingress is
implemented. Current bindings report identity verification only.

## Invalidation and recovery

`global_app_target_reverification_required` (503) means a registry entry is
disabled or its generation changed. Enable the correct endpoint and repeat the
same attachment with a current regional key. Registry changes during a proof
are rejected atomically; no partial account mapping or target is committed.
Repeating an attachment always performs a fresh ownership probe, even with a
reused `Idempotency-Key` header. The response is never replay-cached.

`global_app_target_verification_failed` (502) covers unreachable cells, rejected
credentials, mismatched cell identity and invalid proof responses. Diagnostics
omit remote bodies and transport errors because they could echo the key.
`global_app_directory_unavailable` (503) indicates the gate is disabled or a
regional proof endpoint lacks a configured cell identity. Ownership failures
hide foreign global apps with 404. Identity replacement returns 409.

`gregale global-app detach global-shop --region eu-fsn` removes directory
metadata only. It does not delete the regional app or the immutable account
mapping. Explicit detachment releases the app UUID claim; a subsequent
attachment must prove ownership again. Directory migrations refuse downgrade
while registrations or registry entries remain populated.

The portable acceptance tests exercise two independently authenticated TLS
regional APIs and a separate directory API. Private PostgreSQL tests verify
quota races, ownership uniqueness, generation fencing in both lock orders,
SQL identity/scope guards and startup refusal. They do not qualify native VM
recovery or end-to-end ingress failover.
