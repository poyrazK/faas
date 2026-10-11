# HTTP Operations preview preparation and rollback

This runbook describes the local, unlaunched ADR-521 implementation. No production
cohort has been installed. The local PostgreSQL/HTTP/SDK fixture substitutes a
local HTTP bridge for a VM and cannot qualify native boot, park/restore or leaks.

## Before enabling a cohort

Qualify the exact source on a dedicated x86_64 Linux KVM host with native
acceptance and `test-metal`/`leakcheck`. Cover scheduler restart and uncertain
dispatch, runtime workload identity, artifact retention, cancellation and
reconciliation. Measure ordinary mutating ingress with the closed gateway
resolver: operation metadata resolution adds database work. Qualify store outages
and fleet rollback as well as the portable tests. A host is not currently
available; customer activation remains pending these results.

Record the source and binary hashes, migration state and native receipts. Verify
the existing Operations, invocation and private-copy migrations are installed
once. Do not install duplicate migrations from the preserved full-feature drafts.
Prepare one app/environment and individually identified customers on a plan
with an Operations allowance. Check consumer authentication, platform-tenant
binding, request listener, completion destination and retained result storage.

apid needs the public JWKS matching the workload signer, the existing platform
artifact backend and its cleanup worker. Runtime progress and artifact reports
use audience `gregale:operations`. Keep these dependencies configured through
rollback. Plan allowances, input/output schemas, idempotency retention and
artifact accounting remain unchanged by a cohort grant.

## Prepare with admission closed

The following TOML settings are review templates, not installed configuration.
Use an operator-controlled directory outside customer-writable storage. The
policy must be a readable regular file, without group/world write permissions;
0600 is recommended. Use the same policy contents on every serving apid and
gatewayd-internal node. apid accepts the workload trust environment overrides
documented in [Operations](../operations.md); the preview path has no environment
or boolean override.

```toml
# apid.toml
operations_preview_policy_path = "/etc/gregale/operations-preview.json"
operations_workload_jwks_path = "/etc/gregale/workload-public-jwks.json"
operations_workload_issuer = "https://identity.gregale.dev"
```

```toml
# gatewayd-internal.toml
operations_preview_policy_path = "/etc/gregale/operations-preview.json"
```

Check the issuer against the configured workload signer; do not copy an example
issuer without checking it. Start with this policy:

```json
{"version":1,"enabled":false}
```

An empty path also closes admission. A missing or malformed policy closes new
admission without preventing retained services from restarting. A relative path
is a startup configuration error. Configuring a preview path without workload
trust is an apid startup error; configuring it without durable storage is a
gateway startup error. Configuring trust alone never enables a cohort.

Verify authenticated definition registration and submission return 503 while
closed. A previously declared mutating HTTP operation route must return the
closed response and never invoke the ordinary handler. Confirm ordinary traffic
and retained status/events/downloads remain usable. No admitted execution should
be created by these closed requests.

## Prepare an enabled policy for review

Replace every placeholder below with a canonical nonzero UUID, the exact
environment and UTC timestamps. The template is intentionally invalid until
reviewed. `not_before` is inclusive and `expires_at` is exclusive; their interval
must be positive and no longer than one hour. The entire file is at most 64 KiB,
with at most ten unique account/app/environment cohorts and ten unique customers
per cohort. Wildcards, unknown fields and duplicate JSON members are rejected.

```json
{
  "version": 1,
  "enabled": true,
  "not_before": "<UTC START RFC3339>",
  "expires_at": "<UTC END RFC3339>",
  "cohorts": [{
    "account_id": "<ACCOUNT UUID>",
    "app_id": "<APP UUID>",
    "scope": "<EXACT ENVIRONMENT>",
    "platform_tenant_ids": ["<CUSTOMER UUID>"]
  }]
}
```

The app/environment grant allows definition registration and source deployment
for that cohort. It does not grant another customer access. Identity comes from
existing account and tenant authentication, never request JSON or reserved
headers. Admit a selected customer's export with a stable idempotency key;
equivalent input returns the same operation and conflicting input returns 409.
Verify progress and resumable events, output validation, a retained download
after source deletion, and separate completion-delivery retries. Confirm a
customer and environment outside the cohort are denied and all plan bounds still
apply. Save the operation ID and receipt for rollback verification.

Policy replacement must be atomic on each node: create a private staging file
in the same directory, then rename it onto the configured path. Do not edit the
live file in place or cache an open policy. This implementation does not provide
a fleet-wide atomic replacement, configuration distributor or automatic renewal.
An expired window closes admission until another explicit bounded grant is
installed. Keep node clocks synchronized.

## Roll back admission

Replace policy on **every serving node** with version 1, enabled false. This
example is a future operator action, not a command executed by local qualification.
It preserves same-directory rename and private permissions:

```sh
operations_policy_path='/etc/gregale/operations-preview.json'
operations_policy_stage="$(mktemp "${operations_policy_path}.XXXXXX")"
chmod 0600 "$operations_policy_stage"
cat > "$operations_policy_stage" <<'JSON'
{"version":1,"enabled":false}
JSON
mv -f "$operations_policy_stage" "$operations_policy_path"
```

Verify new API submissions, definition registration, Operations source deploys
and declared HTTP routes are closed on each node. File removal also fails closed;
an explicit disabled file makes operator intent clearer. Leave the gateway
resolver, workload trust, scheduler, artifact backend, cleanup and delivery
workers running. Do not remove route wiring, mutate durable outcomes, delete
retained copies or restart work merely because a delivery is pending.

An admission decision taken before the replacement may still commit. Inspect
accepted executions and use the existing cancellation/reconciliation contract
if intervention is needed. Closing policy does not cancel accepted work or
block explicit recovery. In particular, an uncertain external effect requires
evidence or an explicitly safe retry, not blind repetition.

While closed, observe the recorded operation through status and events. Allow
its existing handler to report progress and attach its result, then verify the
retained download and independent webhook retry. A successful business result
must remain successful during a delivery outage. Reopening the same bounded
cohort and replaying the original key must return the original operation. Save
per-node closure evidence, accepted-work outcomes and any reconciliation receipt.
