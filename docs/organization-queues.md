# Organization queues

An organization's queue bindings, backlog, retry history and worker receipts
belong to the company. Removing or erasing the creator leaves their queue IDs
and accepted work available to teammates and running application workers.
Complete organization billing adoption before changing company queues.

Use `/v1/orgs/{organization}/apps/{slug}/queue-bindings` to configure bindings
and `/v1/orgs/{organization}/apps/{slug}/queues/...` to send, inspect and process
messages. Existing application URLs use the same ownership checks. An
organization URL must name the application's actual organization.

| Role | Queue access |
| --- | --- |
| Owner, admin | Configure and operate queues; inspect payloads; replay dead letters |
| Developer | Configure and operate queues; inspect payloads |
| Viewer | Read binding metadata, status and queue counters |
| Billing | Read aggregate organization queue usage |

API key scopes and MFA requirements also apply. Use an organization-bound key
for teammate automation. Personal keys cannot cross organization ownership.
Membership removal and role changes invalidate requests already in progress,
including requests paused before their intent write or idempotent replay.

```bash
curl -X POST "$GREGALE_API/v1/orgs/acme/apps/image-worker/queues/jobs/leases" \
  -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"max_messages":4,"wait_seconds":20}'
```

The [pull lease contract](queue-pull-leases.md) applies to organization URLs.
A running worker's `gregale:queues` workload token remains application
authority after its creator leaves. Receipts keep their existing attempt,
replay-generation and expiry checks. A former member's human credentials
cannot acknowledge those receipts; an authorized teammate or worker can.

`GET /v1/orgs/{organization}/queue-usage` reports aggregate binding, pending,
processing and dead-letter counts across company applications. It also reports
reserved async invocations and current plan limits. Owners, admins, developers,
viewers and billing seats can call it with `usage:read` or `admin`. Personal
queues are excluded.

After billing adoption, reserved async invocations share the organization's
concurrency allowance across creators. Adoption moves outstanding company
reservations while preserving personal reservations. Each reservation retains
the counter it must release. Pull leases retain their binding concurrency;
queue depth remains an application limit. Limits come from the organization's
current plan.

Stage configuration uses
`/v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings`.
Replacing the complete collection requires the current workload revision and
respects protected environments. Existing deployments retain their pinned
consumer definitions and receipt authority.

The Go, Node and Python SDKs expose these organization operations. For example,
Go provides `LeaseOrgQueueMessages`, `AckOrgQueueLeases`,
`GetOrgProjectEnvironmentQueueBindings` and `GetOrgQueueUsage`.

This review covers private queue consumer projections. External trigger
administration, simple queue-workload profiles, GitOps and full environment
cloning retain their separate access restrictions. An unreviewed company
trigger still blocks creator erasure. Retire active bindings before deleting
the organization. The schema change requires a coordinated restart of async
quota writers while admission and dispatch are paused. Rollback must precede
creator erasure and requires draining organization async reservations.

Legacy `/v1/triggers` administration and inventories operate on personal
applications. Company queue consumers use the reviewed queue-binding APIs.
