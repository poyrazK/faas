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
gregale environment branch <project> <name> [--from <environment>] [--ttl <duration>]
gregale environment branch <project> --list [--from <environment>]
```

`environment` is the command tree's name for `projects environments`
([ADR-20261026145832](20261026145832-cli-command-tree.md)), so the verb is
`projects environments branch` in the manifest. A branch is deleted with the ordinary
`environment delete`, which retires the branch's deployments first. `--at` and `--egress`
arrive with the slices that use them.

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

### 2. Creating a branch is metadata only

The create request makes no provider call, boots nothing and copies no data.

1. **One PostgreSQL transaction** (the plain clone transaction, `cloneProjectEnvironmentOnce`)
   inserts the environment, copies the same config rows `CloneProjectEnvironment` copies
   today, and inserts the branch row. Depth and the account's branch cap are checked
   inside it. The branch's copied workload specs get `min_instances = 0`: a branch keeps
   no warm floor of its own.
2. **One short transaction per parent workload** then creates the branch deployment
   through the ordinary `createDeployment` path. It reuses the parent's selected live
   artifact (ADR-590's `projectCloneArtifact` selection: same rootfs key, digest and
   sidecar layers, no build) and locks the parent's layer keys so layer GC either sees
   the new reference or the branch refuses the app. The row is created in
   `snapshotting` at 100% of the branch and recorded in
   `project_environment_branch_deployments`, with its parent deployment as lineage.
3. apid sends each deployment to schedd's `snapshot_prime`. For a request or service
   workload created by a branch, `Prime` verifies the layer signature and sends
   `deployment_ready` with `no_snapshot_reason = environment_branch` without booting.
   imaged admits that reason only for a deployment in the lineage table, and the row goes
   live. Workers keep the normal prime, since they run continuously. A lost notification
   is recovered by schedd's prime recovery, which re-primes rows left in `snapshotting`.
4. If any deployment cannot be created, apid deletes the branch.

Managed PostgreSQL and managed object storage are copied after the transaction by the
branch data reconciler: databases as point-in-time copies (section 4), buckets as overlay
buckets (section 5). Either leaves the branch's data pending, and its workloads dark,
until the copies are bound.

**Target:** apid-side p99 under 100 ms for a project at the plan's app cap. Rows are
bounded by existing per-project caps.

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
after_restore: { path: /internal/after-restore, identity: reload }
```

The opt-in lives on the existing `after_restore` hook (`AfterRestoreHook.Identity`), since
a reload needs the hook.

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
- **Ship gate: a credential-free capture** (decided 2026-11-03). A hook that
  acknowledges without reloading must not be able to reach the parent's data with the
  parent's credentials. Refusing those credentials on the network is not possible today:
  guests reach Neon endpoints directly, Neon endpoints share proxy addresses, guest egress
  filters by IP only, and the S3 gateway cannot tell which instance calls. So a branch
  never restores memory that held them:
  - Branches restore only from a **credential-free capture** of the parent deployment.
    It is an init-tier capture of a boot that receives placeholder values for every
    managed binding secret (database URLs, bucket keys): syntactically valid, pointing
    at a `.invalid` host, never a real credential. That boot also runs under a
    non-production workload environment (`branch-capture`), so any identity token it
    holds is refused by production-only integrations (#6044). Customer secrets keep
    their values; a branch copies them anyway (section 6).
  - The capture is a row of its own table, `environment_branch_captures`, one per
    parent deployment (`requested`, `ready` or `failed`, leased while it is made), not a
    `snapshots` row. **Production never restores it**, by construction: about thirty
    queries, restore checks and replica paths read `snapshots`, and none can select a row
    that is not there, so a production wake cannot come up with placeholder credentials.
    A branch restores from no other capture. A managed secret's placeholder keeps a URL's
    scheme and points it at `credential-free.invalid`; any other value becomes a fixed
    placeholder (`state.CredentialFreeValue`).
  - **Made lazily** (decided 2026-11-03). The first branch wake of an opted-in app
    cold-boots as on the default path and queues the credential-free capture of the
    lineage parent. Later wakes, in any branch of that parent deployment, restore from
    it. No capture is made for an app nobody branches, and a new parent deployment
    starts without one.
  - Opting in (`after_restore.identity: reload`) therefore also means the app does not need
    working managed connections during init: it connects after the after-restore
    reload, or lazily. An app whose init needs its database fails the capture boot's
    readiness; the capture is then marked failed for that deployment and its branches
    keep cold-booting.
  - What this does not cover: a customer secret that itself holds a production
    credential reaches the branch by value, on either path. That is section 6's rule for
    every branch and stage.

### 4. PostgreSQL: Neon branches now, our own storage later

As built in slice 2:

- **Provider seam.** No new interface is needed. A branch copy is an ordinary managed
  PostgreSQL point-in-time restore (`managedpostgres.Service.RestoreWithResult`), which
  calls the backend's `Provider.Restore`. On Neon that is a branch in the source's Neon
  project (`init_source=parent-data` at `parent_timestamp`). Gregale's own copy-on-write
  storage would implement the same `Provider.Restore` in a later ADR.
- **Creation stays instant.** The branch transaction only counts the parent's managed
  PostgreSQL credentials. If there are any, the branch starts with `data_state = pending`
  and copies nothing; the parent's managed credentials are never copied into the branch.
- **Branch data reconciler** (apid, every 5 s, leased per branch so replicas don't
  collide):
  1. records each copy in `project_environment_branch_databases` before restoring it,
     under the deterministic name `br-<slug>-<hash(environment, source)>`;
  2. restores the source as of the branch point;
  3. binds the copy into the branch's scope once per parent binding, with fresh
     credentials and the same environment key and access;
  4. marks the data `ready` and primes the branch's deployments.

  Every step is idempotent, so a retry resumes. Retries back off exponentially (5 s to
  10 min, at most 20 attempts). Quota, unsupported-restore and invalid-request errors
  fail the data at once, with a message that carries no provider text.
- **Activation waits for the data.** This replaces the wake-path wait. schedd's no-boot
  activation and imaged's acceptance both require `data_state = ready`, so a workload
  never boots without its binding URLs; requests before then find no live deployment in
  the branch. The connection fence (`pkg/managedpostgres/connectionfence`) refuses
  connections rather than holding them, so it isn't used.
- **Deletion.** Copy rows have no foreign key to the environment. After a branch is
  deleted or expires, the reconciler claims each orphaned copy and deletes its bindings,
  then the copy, then the row. A copy whose restore never recorded its ID is found by
  name.
- **Accounting.** For now a copy is an ordinary database: it needs point-in-time restore
  (Pro and Scale) and a free slot under the plan's database limit, and it counts toward
  pooled storage and CU-hours. A separate `EnvironmentBranchDatabases` allowance, and a
  check of Neon's per-project branch limit, are follow-ups.
- **Isolation.** Writes stay in the branch, and the parent database is never touched.
  Data never merges back.

### 5. Object storage: overlay buckets in the S3 gateway

Each bucket bound in the parent gets an **overlay bucket** in the branch. It is a real,
empty bucket in the branch's scope with its own physical name and its own credential,
shaped like the parent's binding (same label, permission and secret prefix). Its link to
the parent is a row in `object_bucket_overlays` (source bucket, consistency, `as_of = T`),
a new table, so an older gateway or apid ignores it. Branch creation stays metadata only.
The branch data reconciler (section 4) reserves each overlay bucket the way a customer's
new bucket is reserved, in the parent's region and through the placement pool
(`object.Service.ReserveRegionBucket`), so it never bypasses a backend's bucket ceiling.
It writes the overlay row before provisioning the bucket, then binds it into the branch's
scope (`pkg/apid/apidserver/environment_branch_objects.go`). No object is copied.

The overlay lives in s3-gatewayd behind credential resolution, since a credential is
bound to exactly one bucket (`pkg/s3gateway/overlay.go`):

| Operation | Behaviour |
|---|---|
| GET/HEAD | Serve the branch's own object. On a branch 404 that is not a delete marker, and with no tombstone for the key, read through to the parent's current object through the parent's own backend, which may differ from the branch's. A read-through returns no version ID. |
| PUT, multipart | Write to the branch only. |
| DELETE (single and bulk) | Write a tombstone, then delete the branch's own object. The tombstone lands first, so the parent's object never shows through in between. The parent is never deleted from. A delete of a version ID is branch-only. |
| ListObjectsV2 | Merge the branch's entries over the parent's (`listOverlayObjects`). The branch wins a key both hold, tombstoned parent keys are skipped, and a page stops at the first truncated list's last entry, so no key is skipped. Continuation tokens are the gateway's own: the start-after boundary of the next page. |
| Copy, version listing, tagging, attributes, multipart listing | The branch's own objects only. |

A read-through is admitted and metered against the branch bucket, which belongs to the
same account. The gateway caches each bucket's overlay for 30 seconds. An overlay never
changes its source, so only a deleted parent bucket can go stale, and a read through to
it finds nothing.

**Consistency.** Each overlay row records its consistency:
- **`live_parent`** (built): a read-through serves the parent's current object, so parent
  writes made after T show through.
- **`as_of`** (later): for parents whose placement supports versioning (today S3 and GCS)
  with versioning observed on, a read-through resolves the parent version that was
  current at T. The gateway serves no other consistency, so a row it does not know reads
  as an empty branch.

**Known limits of `live_parent`.** A parent common prefix whose keys the branch deleted
still lists. A copy whose source exists only in the parent returns 404; a server-side
read-through copy is a follow-up. apid's object API (console and CLI object commands)
shows the branch bucket's own objects only. Overlay data with the parent's objects is
served by the gateway alone.

**Cleanup.** The branch owns each overlay bucket through
`project_environment_branch_buckets`, written before the bucket is reserved. After the
branch is gone, the reconciler revokes the bucket's credentials, empties and deletes it
(an environment-clone bucket is always emptied on delete) and drops its overlay and
tombstones. A bucket reserved before its row recorded it is found by its deterministic
name, which includes the branch environment's ID, so a branch re-created with the same
slug never adopts its predecessor's bucket.

**Direct-to-provider presigning.** It stays refused for overlay buckets. Today the one
direct path is job-artifact download (`pkg/apid/apidserver/job_managed_objects.go`).
Overlay data must always pass through the gateway.

### 6. Networking and identity are scoped to the branch

A branch is a project environment whose deployments run under the branch's scope, so
the stage-isolation fixes apply to it unchanged. Each keys on the caller deployment's
scope, which for a branch is its slug: never `default` (reserved for every environment)
or `production` (refused for branches).
- **Service calls and DNS** (#6034). A service call resolves to the target's deployment
  in the caller's environment. A branch has one for every parent workload (section 2), so
  branch calls stay in the branch. A branch caller whose target has no branch deployment
  is refused, not sent to production. PR previews keep their separate-app namespace; a
  branch needs none.
- **Private networks** (slice 4, as built). An attachment is per app, and vmmd applies an
  app's attachment to every live instance of the app, at boot and on every replay of the
  attachment. A branch instance would therefore join the parent's network. Until vmmd can
  keep an instance out of its app's network, branches and private networks do not share
  a project: branching a project with an attached workload is refused, and so is
  attaching a workload while its project has branches. Both answer 409
  `environment_branch_private_network_unsupported`. The store checks both sides under the
  project's app-row locks, so neither can slip in beside the other. The follow-up lets
  branch instances start without the parent's attachments: a per-instance exclusion
  carried from schedd to vmmd and kept across vmmd adoption, which needs `test-metal`.
  Opting a branch into a network needs environment-scoped attachments after that.
- **Egress identity** (#6044). Only a production-lane deployment gets the app's static
  egress IP, so branches leave through shared egress. The egress allowlist comes with the
  per-environment workload settings.
- **Workload identity** (#6044). Tokens carry the caller's environment, so cloud trust
  policies can tell a branch from production. Outbound integrations refuse every
  non-production environment, branches included.

`environment_branches_enabled` stays off until #6034 and #6044 merge.
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
  - `EnvironmentBranches` (live branches per account): Free 1, Hobby 2, Pro 5, Scale 20,
    the same as preview environments. A branch boots nothing until called, so the cap
    bounds rows and parked deployments, not running compute;
  - `EnvironmentBranchMaxDepth` = 2;
  - `EnvironmentBranchDefaultTTL` 7 days, `EnvironmentBranchMinTTL` 1 hour,
    `EnvironmentBranchMaxTTL` 30 days;
  - `EnvironmentBranchDatabases` arrives with slice 2.
- **Organizations.** The branch routes are not yet in the reviewed project-access table
  (`pkg/apid/projectaccess/routes.go`), so shared organization projects cannot branch, the
  same as full clones today. Adding them is a follow-up once the routes are reviewed.
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
| 2 | PostgreSQL branches: branch data reconciler (restore at T, bind into the branch, data-gated activation), orphaned-copy deletion. A separate database allowance is a follow-up. | The copy is restored at T and bound with new credentials. Workloads stay dark until it exists. The copy and its binding are gone after delete. |
| 3 | Overlay buckets with `live_parent` consistency, created by the branch data reconciler; overlay-bucket cleanup. `as_of` and read-through copy are follow-ups. | Read-through, write isolation, tombstones, merged listing. Parent unchanged. Nothing copied at branch time. The bucket and its credential are gone after delete. |
| 4 | Network and identity scoping. Service calls, egress and identity come from #6034 and #6044, which key on the deployment scope. Built here: branches and private networks refuse each other per project. Branch instances without the parent's attachments are a follow-up. | A branch caller never reaches a production service or private network. |
| 5 | Restore with a new identity (opt-in) from a lazily made credential-free capture of the parent deployment (section 3). Splits into 5a, the capture (placeholder managed secrets, `branch-capture` identity, its own `environment_branch_captures` table that production lookups cannot read), and 5b, the restore (tmpfs identity file, digest acknowledgement, lineage checks). Both need `test-metal`. | No real parent credential in any memory a branch restores. Production never restores a credential-free capture. Identity digest acknowledged; a wrong or missing digest cold-boots. |
| 6 | `contained` and `simulated` egress. | Rehearsal conformance against a branch. |

## Consequences

- Branch creation costs one transaction. Each layer pays its own cost on first use, and
  only for apps that are actually called: a cold boot, a Neon branch, overlay misses.
- ADR-590 full clones stay. They give long-lived, fully primed, data-copied stages. A
  branch gives speed and pays at first use.
- PR previews can later become branches of production, one per pull request. That needs
  its own ADR.
- The opt-in restore path adds an app contract (`after_restore.identity: reload`). Until an app
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
