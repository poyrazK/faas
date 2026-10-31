# ADR-20261031193635: Environment branches — lazy, copy-on-write project environments

Date: 2026-10-31
Status: Proposed
Owner: apid (project environments); schedd and vmmd (branch compute); managed PostgreSQL; s3-gatewayd (overlay buckets)
Amends: [ADR-20261026145832](20261026145832-cli-command-tree.md) (glossary: `branch` as a verb on `environment`)
Related: [ADR-590](590-complete-project-environment-clones.md) (full clones; branches reuse its artifact copy, table registry and promotion), [ADR-732](732-production-forks.md) (restore into a new instance, drive secret scrub), [ADR-348](348-customer-after-restore-hook.md) (after-restore hook), [ADR-222](222-in-process-secret-reload.md) (in-process secret reload), [ADR-155](155-provider-neutral-managed-postgres.md) and [ADR-20261031091607](20261031091607-managed-postgres-ga.md) (managed PostgreSQL), [ADR-20261031122757](20261031122757-s3-gateway-on-compute-nodes.md) (S3 gateway on compute nodes), [ADR-20261019120000](20261019120000-rehearsal-runtime-hot-path-isolation.md) and the scenario rehearsal ADRs (contained network, simulation), [ADR-20261027120000](20261027120000-multi-service-projects-ga.md), #6034, #6037, #6044

## Context

Test environments are turning into simulation environments: an agent, a pull request or a
person wants a full copy of production that they can break, and they want it now. That
needs every layer to branch, not only a sandbox: compute, databases, buckets, networking
and identity. Creating the branch has to take milliseconds, and each layer has to fill in
on first use.

Where Gregale stands on main (`a445de27c8`):

| Layer | Today |
|---|---|
| Config | `environment create --from` copies variables, secrets, secret references, routes and edge policies in one store call (`pkg/state/pgstore_project_environment_clone.go`). It creates no deployments. |
| Compute | ADR-590 full clones reuse the source's built artifact, then prime every target eagerly (cold boot, capture, park) and publish only when all targets are ready. Full clones are dark behind two runtime keys. ADR-732 forks restore a live capture into a quarantined, non-serving, network-less instance. |
| PostgreSQL | An isolated environment clone restores each database through Neon point-in-time branching (`init_source=parent-data` at `parent_timestamp`), *synchronously inside the request*. |
| Object storage | Isolated cloning is refused (`errIsolatedObjectStorageCloneUnsupported`); only `--share-resources` exists. |
| Networking | Private-network attachments are per app (`UNIQUE (app_id)`); networks and peerings are account-level and shared. Only PR previews get an environment-scoped service namespace (`cmd/gatewayd-internal/service_proxy_resolver.go`). #6034 keeps service calls inside the caller's environment. |
| Identity | API keys and org memberships are account identity. #6044 adds the environment to workload identity and refuses outbound integrations to stages. |
| Side effects | Stages don't fire crons (`cronDeploymentDisposition` discards non-default scopes), don't activate queue consumers, and workflows are production-only. |

Nothing here branches in milliseconds. ADR-590 can't get there either, because its design
copies everything up front: it primes every workload, waits 30 s for the data point to
settle, restores databases before it publishes, and fills buckets by copying a manifest.
That is the right design for a long-lived stage. It is the wrong one for a branch that
lives for an hour.

Decisions the owner took on 2026-10-31:
1. a new lazy primitive, not a faster ADR-590;
2. hybrid compute;
3. Neon now, behind an interface Gregale's own copy-on-write storage can replace later;
4. live egress by default, like staging;
5. the name `environment branch`.

## Decision

### 1. A branch is a project environment with a parent and a branch point

```
gregale environment branch <name> [--from <environment>] [--at <time>] [--ttl <duration>] [--egress live|contained|simulated] [--project <slug>]
```

- `--from` defaults to `production`.
- `--at` defaults to now. It is the **branch point** T: every data layer reads the parent
  as of T.
- `--ttl` defaults to 7 days, with a range of 1 hour to 30 days. Previews use the same
  range.
- `--egress` defaults to `live` (section 7).

The API is `POST /v1/projects/{project}/environments/{environment}/branches`. It returns
`201` with the new `ProjectEnvironment`. That response carries a `branch` object holding
the parent, the branch point, `expires_at`, the egress mode, and the state of each data
layer. There is no long-running operation to poll. Listing, reading and deleting use the
existing environment routes.

A new table `project_environment_branches` is keyed by `environment_id` and holds:
- the parent environment and branch point;
- `expires_at`;
- the egress mode;
- the depth;
- the creator.

A branch can be branched again, up to a depth of 2. While a branch has live children it
cannot be deleted; it expires only after its children do. Branches are never `protected`.

### 2. Creating a branch is one metadata transaction

The create request makes no provider call, boots nothing and copies no data. One PostgreSQL
transaction:
- inserts the environment and branch rows;
- copies the same config rows `CloneProjectEnvironment` copies today;
- for each app with a live deployment in the parent, inserts a branch deployment that
  reuses the parent deployment's artifact. That is ADR-590's `projectCloneArtifact` path:
  same rootfs key and digest, no build. The deployment is **published live, unprimed**;
- writes a release-set pin for those deployments;
- writes one database intent per managed PostgreSQL binding and one overlay bucket per
  object-storage binding (sections 4 and 5), plus fresh binding credentials for each;
- enqueues the database work through an outbox, so provider calls start right after
  commit.

**Target:** apid-side p99 under 100 ms for a project at the plan's app cap. The slice-1
acceptance test measures it on PostgreSQL. Rows are bounded by existing per-project caps.

### 3. Compute: cold boot by default, restore the parent's capture by opt-in

**Default path.**
- The first request to an app in a branch takes today's unprimed wake: reason
  `no_snapshot`, cold boot from the parent's artifact, booted with the branch's env,
  secrets and bindings.
- From then on the branch captures its own init and warm snapshots, so later wakes take
  the normal snapshot path.
- Cost: one cold boot per app, on first use, and none for apps never called.

**Opt-in path: restore with a new identity.** An app that declares:

```yaml
lifecycle:
  after_restore: { path: /internal/after-restore }
  restore_identity: reload
```

wakes in a branch from the parent deployment's **init-tier** capture instead of cold
booting. Rules:
- **Init tier only, never warm.** A warm capture holds the parent's request data.
- **Delivering the new identity.** Restored memory still holds the env and secrets from
  the parent's boot, because guest-init reads them only at cold boot
  (`guest/init/main_linux.go`). vmmd therefore writes the branch's env, secrets and
  binding URLs to a tmpfs file, like ADR-222's `FAAS_SECRETS_FILE`. The after-restore
  request carries that file's path and an identity digest.
- **Acknowledgement.** The hook must re-read config, reconnect, and answer with the same
  digest.
- **Fallback.** No hook, a wrong digest, or a timeout all count as ADR-348's
  `after_restore_failed`: the VM is killed and cold-booted from the same artifact under
  the branch identity.
- **Lineage.** A branch deployment records its parent deployment as
  `restore_lineage_deployment_id`. Snapshot lookup (`pkg/sched/wake_snapshot_reads.go`),
  the router ownership check (`pkg/sched/vmmrouter.go`) and vmmd's checks accept a
  capture from that lineage parent and from nowhere else. The runtime-config receipt is
  replaced by the branch's own receipt after the acknowledgement.
- **Instance setup.** ADR-732's restore-into-new-instance placement and drive secret
  scrub (`pkg/fcvm/fork_scrub.go`) apply. Each restore also gets a fresh IP, network
  namespace, jail user and RNG reseed, as today.
- **Ship gate (slice 5).** This path ships only once the parent environment's managed
  binding credentials are refused from branch instances. A hook that acknowledges
  without reloading must not be able to reach the parent's data with the parent's
  credentials.

### 4. PostgreSQL: Neon branches now, a branch provider interface for later

- **Interface.** `managedpostgres` gains an optional `BranchProvider` capability with
  three operations: create a branch of a database as of T, inspect it, and delete it.
  Neon implements it on the existing point-in-time restore (a Neon branch in the source's
  Neon project, `init_source=parent-data`). Gregale's own copy-on-write storage would
  implement the same interface in a later ADR.
- **Creation.** The branch database is created asynchronously by the managed PostgreSQL
  reconciler, which today sweeps every 15 s. Branch creation starts from the outbox right
  after commit, so the database is usually ready before the first request.
- **Waiting on first wake.** A branch deployment that has a pending database **waits in
  the wake path** until the database is ready, up to the app's cold-wake wait budget. It
  then answers `503 environment_branch_data_pending` with `Retry-After`. It cannot boot
  first and connect later, because the binding URL is read at boot. The connection fence
  (`pkg/managedpostgres/connectionfence`) refuses connections and does not hold them, so
  it is not used here.
- **Accounting.**
  - Branch databases don't count toward the plan's database limit. They have their own
    allowance, `EnvironmentBranchDatabases`.
  - They do count toward pooled storage and CU-hours, as ADR-20261031091607 meters them.
  - The fleet's branch count per Neon project must stay under Neon's per-project branch
    limit. Verify that limit against the Neon plan before setting caps.
- **Isolation.** Writes stay in the branch, and the parent database is never touched.
  Data never merges back.

### 5. Object storage: overlay buckets in the S3 gateway

Each bucket bound in the parent gets an **overlay bucket** in the branch. It is a real
bucket row with its own physical name, its own credentials,
`overlay_parent_bucket_id`, and `overlay_as_of = T`. The overlay lives in s3-gatewayd
behind credential resolution, since a credential is bound to exactly one bucket
(`pkg/s3gateway/credential_cache.go`).

| Operation | Behaviour |
|---|---|
| GET/HEAD | Serve the branch's own object, or 404 on a branch tombstone. Otherwise read through to the parent (`prepareGatewayRead`, `pkg/s3gateway/version_reads.go`). |
| PUT, multipart, copy | Write to the branch only. A copy whose source exists only in the parent becomes a server-side read-through copy. |
| DELETE | Write a branch tombstone. The parent is never deleted from. |
| LIST | Merge the branch's keys and tombstones over the parent's listing (`pkg/s3gateway/listing.go`). |
| Version listing | Shows the branch's own versions only. |

**Consistency.** Each overlay bucket records and reports its consistency:
- **`as_of`**: used when the parent's placement supports versioning (today S3 and GCS) and
  versioning is observed on. A read-through resolves the parent version that was current
  at T, cached per key.
- **`live_parent`**: used otherwise. A read-through serves the parent's current object, so
  parent writes made after T show through. The branch says so in its `branch.data`
  state.

**Direct-to-provider presigning.** It stays refused for overlay buckets. Today the one
direct path is job-artifact download (`pkg/apid/apidserver/job_managed_objects.go`).
Overlay data must always pass through the gateway.

### 6. Networking and identity are scoped to the branch

- **Service calls and DNS.**
  - Service calls resolve inside the branch (#6034).
  - Private service DNS gets the branch namespace that PR previews already have.
  - A branch caller with no target in its branch is refused, not sent to production
    (#6034's decision).
- **Private networks.** Branch instances start without the parent app's private-network
  attachments, because attachments are per app and the networks are shared at account
  level. Opting a branch into a private network needs environment-scoped attachments,
  which is a later slice.
- **Egress identity.** Branches use shared egress, never the parent's static IP (#6044).
  The egress allowlist comes with the per-environment workload settings.
- **Workload identity.** Tokens carry the branch environment (#6044), so cloud trust
  policies can tell a branch from production. Outbound integrations are refused to
  branches, as they are to stages (#6044).
- **Credentials.**
  - Every managed binding in a branch gets fresh credentials for the branch's own
    database or overlay bucket, never the parent's.
  - Customer secrets are copied as values, as `environment create --from` does today.
  - Access to a branch is project access, the same as for a stage.

### 7. Egress and side effects: live by default, containment by opt-in

- **`live`** (default) behaves like a stage:
  - outbound traffic is real;
  - crons don't fire;
  - queue consumers stay inactive;
  - workflows stay production-only;
  - inbound webhooks and event subscriptions keep going to production. Their tables are
    keyed by app and have no environment.

  A branch receives only traffic addressed to its own hostnames.
- **`contained`** puts branch instances in the rehearsal contained network: no outbound
  traffic except to platform services in the branch.
- **`simulated`** is `contained` plus the rehearsal simulation broker (HTTP and Resend
  today) and side-effect capture: events, queue messages, webhooks and mail are recorded,
  not sent. `gregale test rehearse` can then target a branch.

### 8. Lifecycle, limits and billing

- **Expiry and deletion.**
  - A sweeper deletes a branch at `expires_at`.
  - Deletion removes deployments and instances, branch databases (through the provider's
    restore-creation delete), overlay objects and tombstones, credentials, secrets and
    release-set pins. It ends with a registry probe that finds nothing left.
  - Project delete stays refused while non-production environments exist (#6037).
- **Limits** (`pkg/api/limits.go`):
  - `EnvironmentBranches` (live branches per account);
  - `EnvironmentBranchDepth`;
  - `EnvironmentBranchTTL` (default and maximum);
  - `EnvironmentBranchDatabases`.

  Plan values come from the COGS model in the slice-1 PR.
- **Billing.** Instance-seconds bill as usual. Branch databases are metered by managed
  PostgreSQL and overlay bytes by the S3 gateway. Data inherited from the parent is never
  billed twice.
- **Diff and promote.** `environment diff` and `environment promote --from <branch>` work
  as they do for stages, on config and release sets only. Data is never promoted.
- **Gating.** apid runtime key `environment_branches_enabled` (default off), productcap
  `environment-branches` (`internal`, then `preview`), and the CLI hidden until admitted,
  as `fullclone.go` does for full clones.

### 9. Slices

| Slice | Delivers | Acceptance |
|---|---|---|
| 0 | Prerequisites: #6034, #6037, #6044 merged; the clone-database cleanup fixes (E1/E2 from the 2026-10-30 stage audit) merged. | Already tracked. |
| 1 | Branch primitive: schema, create/list/delete, TTL sweeper, config copy, unprimed artifact deployments, cold boot on first request, limits, gate. | Create p99 < 100 ms apid-side. First request cold-boots with no build row. Parent deployments, traffic and snapshots unchanged. Delete leaves nothing. |
| 2 | PostgreSQL branches: `BranchProvider` on Neon, outbox start, wait in the wake path, accounting, deletion. | Branch data equals the parent at T. Branch writes never reach the parent. The Neon branch is gone after delete. |
| 3 | Overlay buckets with `as_of` and `live_parent` consistency. | Read-through, write isolation, tombstones, merged listing. Parent unchanged. |
| 4 | Network and identity scoping: branch DNS namespace, private networks off, identity claims. | A branch caller never reaches a production service or private network. |
| 5 | Restore with a new identity (opt-in), after the credential gate in section 3. | Identity digest acknowledged. A wrong or missing digest cold-boots. Parent credentials are refused from the branch. |
| 6 | `contained` and `simulated` egress. | Rehearsal conformance against a branch. |

## Consequences

- Branch creation costs one transaction. Each layer pays its own cost on first use, and
  only for apps that are actually called: a cold boot, a Neon branch, overlay misses.
- ADR-590 full clones stay. They give long-lived, fully primed, data-copied stages. A
  branch gives speed and pays at first use.
- PR previews can later become branches of production, one per pull request. That needs
  its own ADR.
- The opt-in restore path adds an app contract (`restore_identity: reload`). Until an app
  adopts it, its first wake in a branch is a cold boot.
- An overlay on an unversioned parent bucket is not point-in-time. The API reports this
  per bucket, so nobody mistakes `live_parent` for a snapshot.
- Sub-second branch-ready *data* needs Gregale-run copy-on-write PostgreSQL. With Neon,
  creating the branch takes milliseconds, but the first query waits for the provider.

## Alternatives considered

- **Make ADR-590 full clones faster.** Its guarantees are its cost: everything primed and
  every data point settled before publication. Rejected for branches; kept for stages.
- **Restore the parent's capture without an app contract.** The restored process keeps
  the parent's env, including its database URLs. Rejected: a branch could write to
  production.
- **Identity-free captures for app code, like ADR-171's sanitized runtime snapshots.**
  Apps read config at import time, so there is no generic capture point before identity
  enters. Rejected for app workloads.
- **Copy object data eagerly** (the environment-clone manifest copy). Cost grows with
  the data. Rejected for branches.
- **Gregale's own copy-on-write PostgreSQL first.** The owner chose Neon now; the
  `BranchProvider` interface keeps that option open.
- **Contained egress by default.** The owner chose `live`, like staging. Containment and
  simulation are opt-in.
- **`environment fork` or `environment create --lazy`.** `fork` already names ADR-732's
  quarantined debug instances. A flag hides the primitive. The owner chose
  `environment branch`.
