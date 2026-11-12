# ADR-20261110210000: Customer secrets open only on the control plane, under a key held in an HSM

- Status: Proposed (design; implementation lands in slices)
- Date: 2026-11-10
- Amends: ADR 20261022111812 (seal domains: the compute-read domains move
  their readers to the control plane), ADR-178 (fleet identity: retired
  everywhere, not only on compute), ADR-20261023104734 (per-daemon database
  roles: compute loses write access to the grant tables)
- Related: ADR-20261109170000 (signd: the same "key lives on the control
  plane, compute asks over mTLS" pattern), ADR-20261110183000 (Tetragon),
  `docs/runbooks/compute-node-compromise.md`,
  `docs/runbooks/customer-secret-store-compromise.md`,
  `docs/runbooks/release-pipeline-compromise.md`

## Context

Railway's November 2026 incident report describes this chain: customer
secrets, including "sealed" ones, sat in Temporal build histories; one build
credential read 353 other builds' histories; the same credential cloned the
internal monorepo; a misconfigured OIDC binding issued production cloud
credentials; internal SSH keys led to a production machine with a
long-lived credential on disk that decrypted customer secrets. No HSM was
involved.

ADR 20261022111812 already narrowed each decrypt key to the daemons that
read it. A review on 2026-11-10 (origin/main e4d352f42e) checked each link
of Railway's chain against Gregale:

| Railway link | Gregale | Status |
|---|---|---|
| Secrets in build history; one build credential reads other builds | builderd never opens ciphertext; the build VM opens `sealed_env` itself as a BuildKit secret mount; the cache key includes the ciphertext hash; a build holds no platform credential (Hunts 30–31) | not exposed |
| Build credential clones internal source | the build VM gets no GitHub token | not exposed |
| OIDC binding issues prod cloud credentials | GitHub OIDC → GCP workload identity (`gregale-builder-ci`, `gregale-fleet-reader`); the provider's `attributeCondition` is set in GCP and could not be read with a read-only account | **unverified** |
| CI or SSH access → prod machine → on-disk decrypt key | see 1–3 below | **exposed** |
| No HSM | every identity is an age file | **exposed** |

The exposures:

1. **GitHub environments `production` and `production-us` had no
   protection.** They had no branch policy, no reviewers, and admin bypass
   was on. A workflow on any branch that named `environment: production`
   received every secret below. The repository is public. *Fixed in repo
   settings on 2026-11-10 (section 1); a probe job on a branch was refused.*
2. **CI carries every private identity.** `cd-compute.yml` and
   `cd-controlplane.yml` receive `FLEET_AGE_KEY`, every
   `SEAL_DOMAIN_<D>_AGE_KEY`, `COMPUTE_SSH_KEY`, `CP_SSH_KEY`,
   `COMPUTE_DATABASE_URL` and `COMPUTE_SIGN_KEY` as environment secrets, and
   `scripts/ci/seal_domain_vars.sh` writes them to the runner's disk on
   every rollout. Anyone who controls one step of a production rollout
   holds every key that opens customer secrets, plus SSH into every node.
3. **Every compute node holds the keys to every tenant's secrets.** The
   seal-domain split stages `runtime`, `build`, `edge`, `edge-tls`,
   `delivery` and `control` on every compute node. vmmd opens app secrets
   locally (`pkg/fcvm/manager.go` `openBoundEntry`). Compute nodes run
   untrusted guest code. Root on one node, for example after a guest escape,
   therefore decrypts every customer's environment secrets, registry
   passwords, log-drain credentials, webhook secrets and the cluster signing
   key, not just those of the tenants placed on that node. In production
   `fleet.age` is also still on every node: `faas_compute_fleet_key_retired`
   (#6939) is merged but not enabled.
4. **Placement cannot authorize anything today.** `faas_runtime`, the role
   every compute daemon logs in as, has DML on every table
   (`deploy/ansible/roles/postgres/files/runtime-roles.sql`), and
   `instances` rows are written on the node. A rooted node can write itself
   a placement for any app.

"Zero chance of exposure" cannot be literally guaranteed: a node must
eventually hold the plaintext of the secrets of the workloads it runs,
because the guest reads them. The goal this ADR sets is the strongest one
that is true:

> **No single compromise of a compute node, the CI system, a backup, a
> database dump or a control-plane disk yields any customer secret beyond
> those of the workloads that node is legitimately running at that moment.
> The root keys never exist outside an HSM.**

## Decision

### 1. Production deployments come only from `main` and release tags (done)

Applied on 2026-11-10 through the GitHub API:

- `production` and `production-us` accept deployments only from the branch
  `main` and tags matching `v*.*.*`; `can_admins_bypass` is false.
- Ruleset "release tags (v\*.\*.\*) admin-only" (id 26978252) blocks
  creating, moving and deleting `v*.*.*` tags for everyone except the
  repository admin role. The action major tag `v0`, which
  `scripts/ci/advance-action-major-tag.sh` force-pushes, does not match.
- Verified: a job on branch `sec/env-policy-probe` naming
  `environment: production` was rejected ("Branch ... is not allowed to
  deploy to production due to environment protection rules") and received no
  secret. The probe branch and run were deleted.

Deploy runners stay on the control-plane hosts and the repository stays
public (owner decision, 2026-11-10). That makes sections 2 and 6 the real
protection: once no private identity reaches CI, controlling a CI job
gives no customer secret.

### 2. CI never holds a private identity

The private half of every seal domain is created on the control plane,
inside the HSM where section 5 applies, and is never exported in plaintext.
CI keeps only public recipients (`SEAL_DOMAIN_<D>_AGE_RECIPIENT`) and the
non-secret wrapped blobs of section 5.

- **Key escrow (implemented in slice 2).** The private identities live in
  `/etc/faas/secrets/key-escrow/<name>.age` on the control-plane host
  (`0700`/`0400` root, loaded by no daemon). With the environment variable
  `SEAL_DOMAIN_KEY_SOURCE=escrow`, `cd-compute` and `cd-controlplane` fetch
  only the identities the role being deployed needs
  (`seal_domain_vars.sh --identities`), over SSH pinned to
  `COMPUTE_KNOWN_HOSTS`, use them for that step and delete them.
  `scripts/ci/key_escrow.sh` is the only client; identities travel on the SSH
  channel, never in a command line.
- A rollout in escrow mode that still finds a `SEAL_DOMAIN_<D>_AGE_KEY` or
  `FLEET_AGE_KEY` secret fails, telling the operator to delete it. A release
  that predates the escrow fails closed instead of falling back.
- The one-shot workflow `seal-domain-key-escrow.yml` (main only) moves
  today's secrets into the escrow (`put`, never replacing a different
  identity), lists it (`status`), and rotates: `rotate` runs `gregalectl
  seal-domain init` on the control plane, keeps the old identity as
  `<d>.age.previous` (fetched and staged as the role's
  `*_previous_identity_src` for the ADR-057 overlap) and prints only the new
  recipient; `retire` drops the previous identity after the re-seal.
  `docs/runbooks/seal-domain-key-escrow.md` is the procedure.
- The escrow is the key store secretd uses in slice 3 and the HSM wraps in
  slice 4.
- Once every domain has moved, the GitHub secrets `FLEET_AGE_KEY` and
  `SEAL_DOMAIN_*_AGE_KEY` are deleted and the keys they held are rotated:
  they were readable by any production job, so they are treated as exposed.

SSH keys and `COMPUTE_DATABASE_URL` stay in CI for now. Once the decrypt
keys are gone, they no longer reach customer secrets (section 4 removes the
database's write path to the grants).

### 3. A control-plane daemon, `secretd`, is the only opener of compute-read domains

`secretd` runs only on control-plane hosts, like signd
(ADR-20261109170000). It holds, through the HSM, the identities of
`runtime`, `build`, `edge` and `delivery`. It listens on the control
plane's private address over mTLS (pki feature `secret-opener`, client
leaves issued to each node's identity) and serves:

```
OpenWorkloadSecrets(grant, entries[]) -> plaintext[]
```

- `grant` is the placement grant of section 4.
- `entries` names rows, not ciphertext: (table, row id, key). secretd reads
  the ciphertext itself with its own database role, `faas_secretd` (SELECT
  on the sealed tables and the grant table only), so a node cannot hand it
  an envelope from another tenant.
- secretd opens only rows that belong to the grant's app and deployment
  (or release snapshot), checks the account binding
  (ADR-20261023173624), and returns plaintext over the mTLS channel. It
  never logs or persists plaintext.
- Per node, it enforces a rate limit and a cap on distinct apps per hour.
  It records every open (node, app, deployment, keys, grant id) in an
  append-only `secret_open_audit` table, which `faas_runtime` cannot
  write.
- Alerts: `FaasSecretOpenRefused` (grant invalid, node mismatch, row
  outside the grant), `FaasSecretOpenBurst` (a node over its distinct-app
  budget), `FaasSecretdDown`.

vmmd, imaged and gatewayd-internal call secretd where they open locally
today. The domains move one at a time, in this order:

| domain | today's reader on compute | after |
|---|---|---|
| `runtime` | vmmd (`openBoundEntry`, sidecar env, sealed capture keys) | vmmd calls `OpenWorkloadSecrets` at wake; plaintext lives only in the wake request and the guest |
| `build` | vmmd (registry passwords for builder VMs) | builderd's build lease is the grant; vmmd asks secretd |
| `edge` | gatewayd-internal (basic auth, log drain headers, edge rule header secrets) | gatewayd-internal asks per app with a short in-memory cache (TTL ≤ 60 s), with the app's routing as the grant |
| `delivery` | schedd on compute (execution payloads, app webhook secrets, commit sources) | payloads open on the control plane's schedd only; compute schedd receives plaintext for the execution it runs |
| `control` | schedd on compute (cluster signing keys) | signing moves behind signd (as for artifacts); compute holds no cluster signing key |
| `edge-tls` | gatewayd-public | **unchanged in this ADR**: it is the platform's apps wildcard key, not a customer secret, and short-lived (ADR-20261108100000, short-lived edge certificates). Keyless TLS is a later decision. |

**Slice 3a (implemented, dark): the server.** `pkg/secretd`, proto
`onebox.faas.secretd.v1.Secretd`, store `pkg/secretd/secretpg`, and
migration `20261111124927540_secret_grants_and_open_audit`. No binary or
deploy yet; that is slice 3b.

- `OpenAppSecrets(app_id, entries)`: the caller is its mTLS certificate's
  name, resolved to an active `compute_nodes.id` (identity protected since
  slice 1b). It is answered only as the app's owner (`apps.node_id`,
  protected since slice 1a) or as a peer holding an unexpired grant. Every
  envelope must be bound (`secretbox.Binding.Require`) to the app's resource
  account and hold exactly the selected key. The open is written to
  `secret_open_audit` before any plaintext is returned; an open that cannot
  be recorded fails. A node opening more than 1000 distinct apps in a rolling
  hour is refused (`ResourceExhausted`). Every refusal is counted in
  `faas_secretd_refusals_total{rpc,reason}` (loopback `/metrics`, job
  `secretd`): a cross-tenant envelope or capture (`wrong_account`,
  `other_app`) and an exhausted budget page (`FaasSecretdCrossTenantRequest`,
  `FaasSecretdBudgetExhausted`) as compromise signals; repeated
  unauthorized callers warn.
- `GrantPeer(app_id, node_name, ttl)`: only the app's owner may grant; the
  TTL is clamped to 1 min–1 h and renewed by the owner while the peer runs
  the app. `secret_grants` is written only by secretd.
- Neither table is writable by `faas_runtime` (migration and
  `runtime-roles.sql`).
- **Residual:** the request carries ciphertext, and envelopes are bound to an
  account, not an app. So a node that owns any app of an account could ask
  secretd to open ciphertext it read from that account's other apps. That
  stays within one customer and never crosses tenants, and every open is
  audited. Closing it means secretd loads the ciphertext itself by row
  reference (a later slice).

**Slice 3b (implemented, off until configured): the daemon and its deploy.**

- `cmd/secretd` runs on the control plane only (`FAAS_SECRETD_ROLE`), as
  the OS user `faas-secretd`. It logs in to Postgres only as `faas_secretd`:
  peer authentication, never mapped to the owner, a `faas_runtime` member
  with explicit `SELECT, INSERT, UPDATE, DELETE` on `secret_grants` and
  `SELECT, INSERT` on `secret_open_audit`.
- It serves mutual TLS only, on `tcp://<private address>:8098`, which the
  firewall opens to the compute CIDRs. The handshake admits only registered
  compute nodes' leaves (the signd verifier). The pkg/pki feature
  `secret-opener` issues the server leaf `secretd.faas` and, from slice 3c,
  each node's client leaf, whose CN is the node's name.
- The runtime identity reaches it only as a systemd credential, with
  `_previous` beside it during a rotation. CD stages `runtime.age` on the
  control plane only while the GitHub environment variable
  `SEAL_DOMAIN_RUNTIME_ON_CONTROL_PLANE` is `true`, and the seal_domains
  role removes it from a control-plane host when it is not.
- `secretd_service` installs it only where `faas_secretd_listen` is set; CD
  restarts it from the active release. Expired grants are swept every 5 min.

Until slice 3c nothing calls secretd, so turning it on changes no wake.

**Slice 3c-1 (implemented, off until configured): vmmd asks secretd.**
`FAAS_RUNTIME_SECRET_OPENER=secretd` (vmmd_service's
`faas_vmmd_secretd_open`, one node at a time) routes every app-secret
open through secretd: the wake's env entries, sidecar app secrets, and live
secret reload. vmmd still checks apid's writer signatures first, refuses a
partial answer, and needs `FAAS_RUNTIME_SECRET_BINDING_REQUIRED`, since
secretd refuses unbound envelopes. Its client leaf is the `secret-opener`
feature leaf whose CN is the node's name, issued once `faas_secretd_target`
is set.

Still opened on the node with its runtime identity, so the node keeps it
for now: sidecar direct env (`sidecar_env` byte envelopes) and the sealed
crash-capture keys, which are sealed to the node's current runtime
recipient. Both move before node join stops staging the runtime identity
(slice 3d).

**Slice 3c-2 (implemented, off until configured): owner grants.** With
`FAAS_SCHEDD_SECRETD_TARGET` (compute_only_service, from `faas_secretd_target`),
a compute schedd dials secretd with the node's client leaf, handed to it as
a credential, so secretd knows it as the owner node. Before `VMMRouter`
creates or restores an instance on another node it calls `GrantPeer` (TTL
1 h, reused while more than half remains); every 15 min it renews the
grants whose app still has a live instance on that peer (the ledger) and
forgets the rest, which then expire. A failed grant is logged and the
create goes ahead: a peer still opening locally needs none, and a peer
asking secretd is refused with `PermissionDenied`. Rollout: set
`faas_secretd_target` on every compute node (leaves + grants), then
`faas_vmmd_secretd_open` node by node, so peers are granted before any of
them needs it.

**Slice 3d (owner decisions 2026-11-11): nothing a node reads opens without
secretd.** Moving the runtime identity off compute nodes is pointless while
a rooted node can read the same plaintext elsewhere. Three paths remained:

1. **Park snapshots are plaintext.** A snapshot's memory and vmstate hold
   every secret the app loaded, and the shared artifact store they live in
   is readable by every compute node (compute holds objectAdmin on the
   artifacts bucket). *Decision: per-snapshot encryption.* Every snapshot
   is written through the ADR-733 encrypting backend with a fresh age
   identity sealed to the runtime recipient (sealing needs only the public
   recipient, so nodes keep that). The sealed key lives on the snapshot row.
   At restore vmmd asks secretd for the key by snapshot id; secretd reads
   the sealed key from the row, checks the row's app against the caller's
   ownership or grant, audits, opens and returns the per-snapshot identity.
   The restore path already decrypts sealed captures
   (`openSealedSnapshot`). Cost: one secretd round trip per restore plus a
   streaming decrypt; it must be measured on the reference node against the
   350 ms p95 restore target before it is turned on, and a node-local cache
   of opened snapshot keys (memory only, per snapshot, dropped on park) keeps
   the round trip off repeat restores.
2. **Crash-capture keys** (ADR-733) are sealed to the same recipient and
   opened by vmmd (forks) and imaged (staging). They take the same path:
   `OpenCaptureKey(app_id, capture_id)` reads `crash_captures.sealed_key`.
3. **Sidecar direct env** is sealed without an account binding, so secretd
   must not open caller-supplied ciphertext for it (a node could submit
   another tenant's). *Decision: row references.* secretd loads the
   ciphertext from the app's own rows (`deployments.sidecars` of a
   deployment of that app, with its re-seal overrides) and opens it there.
   App secrets move to row references too, which closes slice 3a's
   same-account residual: a node names what to open, never supplies it.

Every key-by-reference RPC follows one rule: **the caller sends ids, never
ciphertext; secretd reads the row, checks that it belongs to the app the
caller owns or holds a grant for, and audits before answering.**

**3d-3 implementation map (VM lifecycle: needs `test-metal`, `leakcheck`
and the restore measurement on a native KVM host).** Reuse ADR-733's
mechanism end to end, so the restore side is already there:

- *Seal id.* The snapshot row does not exist at capture (imaged writes it
  from schedd's `snapshot_written` payload), so the seal id is the capture
  token in the memory key (`state.SnapshotCaptureToken`). The key is sealed
  with crashcrypt's namespace bound to that id, as for crash captures.
- *Capture.* `pkg/sched/application_standard_snapshot_admission.go`
  `captureSnapshotWithStandards` is the one entry for park and warm
  (`engine.go` park and warm, `snapshot_publication.go`); behind a schedd
  flag it passes the token as `seal_capture_id`. `WarmSnapshotRequest`
  already has it; `PauseAndSnapshotRequest` and the native
  `CaptureAdmittedRuntime` grant gain it, and `pkg/vmmdgrpc` sets
  `spec.Storage` from `SealCaptureStorage` as WarmSnapshot does. Sealing
  needs only the runtime recipient, which nodes keep.
- *Record.* `snapshots.sealed_key bytea` and `snapshots.sealed_capture_id
  uuid` (CHECK both or neither), written by imaged's row writer from the
  payload (`snapshotWrittenPayload`). The `.age` twins are what the shared
  store holds.
- *Restore.* Every `SnapshotRef` construction sets `CaptureID`/`SealedKey`
  from the row: `engine.go` wake restore, `warm_pool.go`,
  `execution_claim_resolver.go`, `fork_runtime.go` (already),
  `vmmclient_runtime_admission.go` (builds the proto itself) and
  `vmmclient_snapshot_backing.go`. vmmd's `openSealedSnapshot` then takes
  the key from secretd (`OpenCaptureKey`, extended to resolve
  `snapshots.sealed_capture_id` through its deployment's app).
- *Everything else that touches snapshot objects* must handle the `.age`
  twin: imaged `deleteSnapshotPair` / `cleanupSnapshotCaptures` (delete
  twins), `captureInitOrReuse` (reuse a sealed row's key), ADR-165 memory
  compression, snapshot memory sharing (#6131: encrypted pages do not
  dedupe across snapshots, so measure the size cost), and the snapshot
  scans.
- *Latency.* A per-node in-memory cache of opened snapshot keys (dropped at
  park and on grant expiry) keeps secretd off repeat restores; the decrypt
  is streaming. Turn the flag on only after the reference node's restore
  p95 stays under 350 ms with it.

**3d-4 (implemented): app secrets by reference.** Selection stays in
schedd; what changes is the proof. Each app-secret entry on the wake wire
(`SealedSecret.secret_scope`) and on vmmd's live-reload rows carries the
app_secrets scope of its row, and vmmd forwards it to secretd
(`SealedEntry.scope`). An entry with a scope is a reference: secretd ignores
any ciphertext sent with it and opens the app's own row (app, scope,
source key), still bound to the app's account. This closes slice 3a's
same-account residual for app secrets. Entries without a scope (values
inherited from another app's rows, an older schedd) keep the ciphertext
path until `FAAS_SECRETD_REQUIRE_REFERENCES` (secretd_service's
`faas_secretd_require_references`) refuses them, once every schedd and vmmd
sends references. Writer signatures are still checked on the node.

Order: 3d-1 crash-capture keys (already sealed today), 3d-2 sidecar env by
reference, 3d-3 snapshot sealing at the source + key by reference (behind a
flag until the restore measurement), 3d-4 app secrets by reference, then
node join stops staging the runtime identity and `fleet-seal verify`
refuses a compute node that still holds it.

When a domain has moved, node join stops staging its identity, deletes
leftovers (the `faas_compute_fleet_key_retired` pattern), and
`fleet-seal verify` refuses a compute node that still holds it.

**The other compute-read domains (owner decisions 2026-11-11).** After the
runtime domain, compute still holds `build` (customer registry passwords),
`edge` (basic-auth credentials, log drain headers, secret edge-rule request
headers) and `delivery` (execution payloads, app webhook secrets, commit
source connections). Each moves as follows:

- **3f, log drain headers:** drains already ship only from the app's owner
  node (`newAppLogDrainOwnership`), so gatewayd-internal opens a drain's
  header by reference through secretd, authorized like runtime secrets
  (owner or grant).
- **3g, delivery:** compute schedd opens delivery values by row reference
  through secretd with owner or grant authorization; the delivery identity
  then leaves compute.
- **3h, basic auth:** any node's edge checks basic auth for any app, so
  ownership cannot narrow it. The check becomes verify-only: apid stores an
  argon2id verifier beside the sealed value, gateways compare against it
  (with an in-memory cache of passing credentials keyed by a hash of the
  presented header), and never open the credential.
- **3i, secret edge-rule request headers:** the header is added on the
  instance's node by vmmd's forward path, which opens it through secretd
  with owner or grant authorization for the instance's app. `route` rules
  target only apps of the same account (`target_app_slug`), so every request
  a header rule touches reaches an instance through vmmd and no edge needs
  the value. The edge identity then leaves compute.
- **3j, registry passwords:** builder VMs pull private images through a
  control-plane registry proxy that adds the app's credentials per build
  token, scoped to the app's registries. No compute node or builder VM sees
  the password; the build identity then leaves compute.
- **1c, placement inputs:** each compute node gets its own Postgres login,
  and row security lets it write only its own `compute_nodes` and capacity
  rows.

### 4. Placement grants are issued by the control plane

A rooted node can write `instances`, so secretd cannot authorize from it.
A **placement grant** is a record a compute role cannot write:

```
secret_grants(id, node_id, app_id, deployment_id, instance_wake_id,
              purpose {wake|build|edge}, issued_at, expires_at, revoked_at)
```

- It is written only by `faas_grantor`, which is held by the control-plane
  daemon that admits the placement: control-plane schedd for wakes and
  jobs, builderd's control-plane lease for builds, and the route publisher
  for edge. `faas_runtime` gets SELECT only, which `runtime-roles.sql`
  makes explicit with a REVOKE as it does for `artifact_signatures`.
- secretd accepts a grant only when the mTLS client leaf names the grant's
  `node_id`, the grant is unexpired and unrevoked, and the rows asked for
  belong to the grant's deployment.
- Grants expire (wake: 10 minutes after issue, renewed while the instance
  runs; build: the build timeout). A drained or fenced node's grants are
  revoked with it.

**Finding (2026-11-10): no control-plane admission point exists today.**
Every compute node runs its own schedd (M9,
`pkg/daemonunitspec/registry.go`), and gatewayd-internal wakes through the
owning node's schedd (ADR-20261110120000). An app's owner node is chosen by
whichever schedd wins `ClaimUnplaced` (`pkg/sched/owner_choice.go`,
`ChooseOwnerNode`, "every schedd computes the same answer from the same
rows"), compute schedds included, and the instance row is written by the
node that boots it (`pkg/sched/engine.go` `createInstanceWithWakeRetry`).
Both are rows `faas_runtime` can write. So slice 1 is not "issue a grant at
the existing admission point" but "move owner assignment to the control
plane": the control-plane schedd alone assigns owners (in a table
`faas_runtime` cannot write) and issues grants for them, and compute schedds
only wake apps assigned to their node. A rooted node then receives only
apps the control plane chose to give it: it cannot target a specific
tenant, and per-node budgets bound how many it gets. This is the
prerequisite the rest depends on (owner approved the shape, 2026-11-11).

**Slice 1a (implemented): the owner is the control plane's, enforced by
Postgres.**

- Migration `20261110210850013_app_owner_authority` adds a trigger on
  `apps` (`BEFORE INSERT OR UPDATE OF node_id`). It admits an owner change
  only from a login that is a member of `faas_owner_authority`, a NOLOGIN
  group that `runtime-roles.sql` grants to the control plane's peer-auth
  `faas_schedd` and the owner `faas`, and never to a compute role.
  `session_user` decides, so `SET ROLE` cannot borrow it. Clearing an owner
  is always admitted. The mode lives in `app_owner_authority` (`any`, the
  default, then `observe`, then `enforce`). In observe, every change is
  recorded in the append-only `app_owner_writes`. In enforce, an
  unauthorized change fails with SQLSTATE 42501. Compute logins cannot write
  either table. The function is `SECURITY DEFINER` with
  `search_path = pg_catalog, pg_temp` and schema-qualified tables, so a
  temporary table cannot shadow the mode.
- `FAAS_APP_OWNER_AUTHORITY=control-plane` (Ansible
  `faas_app_owner_authority`) stops compute schedds from claiming unplaced
  apps, pulling orphaned apps onto themselves and pressure-rebalancing. The
  control plane's schedd claims new apps and runs
  `RebalanceOrphanedAppsCentral`, which chooses each orphan's destination
  with `ChooseOwnerNode` among the other admitting nodes with headroom.
  Pressure rebalancing became a release request in slice 1b.
- `faas_app_owner_unauthorized_writes` and `FaasAppOwnerUnauthorizedWrite`
  (runbook `docs/runbooks/FaasAppOwnerUnauthorizedWrite.md`, with the
  rollout: observe, switch, watch, enforce).

**Slice 1b (implemented): release requests and node identity.**

- **Release requests.** Under control-plane authority, a pressured node's
  schedd records `app_owner_release_requests(app_id, from_node_id)` instead
  of moving the app (`Engine.RequestPressureRelease`, only for apps it owns).
  The control plane's schedd processes them every 15 s
  (`ProcessOwnerReleaseRequests`) through the same checks as an owner's own
  pressure rebalance: the app is still the requester's, parked and out of
  cooldown. The control plane chooses the destination, which can be the
  app's overflow preference or any other peer with headroom. A request can
  only make an app leave its owner. A forged row naming the wrong owner is
  consumed without effect (tested).
- **Node identity.** The control plane will map a caller's mTLS certificate
  name to `compute_nodes.id` (slice 3). A compute login that could rename the
  owner's row to its own name would pass that check, so
  `compute_node_identity_check` refuses `UPDATE OF id, name` from logins
  outside `faas_owner_authority` in enforce mode, and logs
  `faas_node_identity_change` in observe. Deleting an owner's row is already
  refused by `apps_node_id_fkey ON DELETE RESTRICT` (not deferrable).

**Placement grants move to slice 3.** The owner's schedd also places
instances on peer nodes (snapshot locality, replica spread). The database
cannot tell which node wrote an `instances` row, because every compute node
shares a login. So secretd decides from the caller's mTLS identity: the
owner, as recorded in the database row that only the control plane can
change, may open its app's secrets. A peer may open them only under a
grant the owner asked secretd for over mTLS, recorded in `secret_grants`,
which compute cannot write.

**Still trusted from compute-writable rows** (residual, for slice 1c):
placement inputs such as `compute_nodes.admission_ceiling_mb` and node
readiness. A rooted node can inflate its own ceiling and so attract a larger
share of new claims and recoveries. It cannot pick a tenant. The fix is a
per-node database identity (certificate-authenticated logins) or
control-plane-owned capacity rows. That choice is the owner's.

What a rooted node still gets: the plaintext of workloads placed on it
while it is compromised, which it could read from guest memory anyway.
What it no longer gets: any other tenant's secrets, any key, or anything
that keeps working after the node is fenced.

### 5. Root keys live in an HSM on each control-plane server

Owner decision (2026-11-10): own hardware HSMs in the bare-metal control
plane hosts (YubiHSM 2 or Nitrokey HSM 2 class), not a cloud KMS.

Neither class of device performs X25519, which age identities use, so the
HSM does not hold the age identities directly. It holds one AES-256 wrap
key per seal domain (`kek/<domain>`), created inside the device and marked
non-exportable except under the backup wrap key of the ceremony below.
Two tiers:

- **Tier A, wrapped identities.** Each domain's age identity exists on
  disk only as `domains/<domain>.age.wrapped`, encrypted under
  `kek/<domain>`. secretd (and the existing control-plane readers:
  outboundd, githubd, meterd, s3-gatewayd, apid for `account`, maild,
  livequeryd) unwraps it at start into locked memory: `mlock`,
  `MADV_DONTDUMP`, `LimitCORE=0`, no swap, and the unit's
  `ProtectSystem`/`NoNewPrivileges` sandbox. Disk, backups, the Ansible
  fact cache and CI never see a plaintext identity. Root on a control-plane
  host can still read a running daemon's memory.
- **Tier B, per-value data keys.** A new secretbox envelope version seals
  each value under a fresh AES-256-GCM data key, and the data key is wrapped
  by `kek/<domain>` inside the HSM. Every open is an HSM unwrap call, so no
  process ever holds a long-lived key that opens more than one value. Root
  on a control-plane host gets an oracle only while the compromise lasts,
  limited by the HSM's throughput, the per-node budgets of section 3 and the
  HSM's own audit log. It cannot take keys away. Data keys of one release
  snapshot share one wrap, so a wake costs one HSM call, and secretd may
  cache an unwrapped data key for at most 5 minutes. Existing rows move to
  v3 with the `sealmigrate` expand/dual-seal/re-seal/contract procedure of
  ADR 20261022111812 §6.

Operations:

- **HA.** Each control-plane server has its own HSM. The same wrap keys are
  imported into both under a backup wrap key at the key ceremony.
- **Ceremony and backup.** Two operators; the backup wrap key is split
  (Shamir 2-of-3, which the device supports for its wrap-key backup) onto
  offline media. The runbook `docs/runbooks/hsm-key-ceremony.md` is part of
  slice 4.
- **Device credentials.** Per-daemon HSM authentication keys whose
  capabilities allow only unwrap for their own domains, loaded with
  `LoadCredential=`. Possession of the device plus one daemon's credential
  opens only that daemon's domains.
- **Unverified until slice 4:** device throughput for AES unwrap, the
  exact capability names, and the Go client library. Slice 4 measures
  these on the chosen hardware before Tier B is designed in detail.

### 6. Out-of-band checks

- **OIDC conditions.** An IAM administrator runs
  `gcloud iam workload-identity-pools providers list --workload-identity-pool=github-actions --location=global --project=<builder project> --format='table(name.basename(),attributeCondition)'`
  and confirms that every provider pins `assertion.repository ==
  'poyrazK/faas'` and, for anything that reaches production data, the
  `main` ref or the release workflow on a `v*.*.*` tag. A CI check that
  reads the condition and fails on drift follows if the read can be granted
  to a CI identity.
- **Enable `faas_compute_fleet_key_retired` in production** (#6939, owner
  rollout). It is independent of the slices and is the largest single
  reduction available today.
- **Rotate everything CI held** once slice 2 lands, because sections 1–2
  describe a period during which any branch could read it.

## Slices

| slice | content | depends on |
|---|---|---|
| 0 | GitHub environment policy + tag ruleset (done, §1); enable `faas_compute_fleet_key_retired` in prod; OIDC condition check | — |
| 1a | Owner authority: apps trigger + `faas_owner_authority`, `FAAS_APP_OWNER_AUTHORITY=control-plane`, central ownership recovery, metric + alert (code in #7003) | — |
| 1b | Pressure release requests, node identity trigger (code in #7003) | 1a |
| 1c-1 | Per-node DB identity, database side (code in #7003): `compute_node_db_logins(login, node_id)` written only by the schema owner; for a mapped login, triggers confine `compute_nodes`, `node_memory_reports` and `compute_node_heartbeats` writes to its own node, refuse changes to its id, name, region, zone, role, dial addresses and certificate binding, and clamp raises of vpcpus, mem_mb, max_concurrency, admission_ceiling_mb and vcpu_budget to the current value (`faas_node_capacity_clamped` in the Postgres log), so vmmd's self-registration keeps working and only the control plane raises capacity. Unmapped logins are unchanged | 1a |
| 1c-2 | Per-node logins: one certificate-authenticated login per node and daemon (pg_hba `hostssl ... cert`, pg_ident mapping the node leaf's CN), created and mapped at node join by the control plane, then the shared compute passwords retired. Residual after 1c: a node still reports its own measured memory and readiness | 1c-1 |
| 2 | CI holds recipients only: key escrow on the control plane, `SEAL_DOMAIN_KEY_SOURCE=escrow`, escrow/rotate workflow, runbook (code in this PR); then the owner escrows, deletes the GitHub secrets and rotates | — |
| 3 | `secretd` + `OpenWorkloadSecrets` (owner by protected `apps.node_id`, peers by owner-requested `secret_grants`) + audit + alerts; move `runtime`, then `build`, `edge`, `delivery`; `control` behind signd; node join stops staging, verify refuses leftovers | 1 |
| 3d | Everything by reference: crash-capture keys, sidecar env, per-snapshot encryption with keys from secretd (latency-measured), app secrets; then the runtime identity leaves compute | 3 |
| 3e | The runtime identity leaves compute (code in #7003): per node `faas_seal_domain_runtime_retired_on_compute` (needs contracted + `faas_vmmd_secretd_open`) removes `runtime.age`/`.previous`, stages `runtime.age.pub`; vmmd `FAAS_SEAL_DOMAIN_RUNTIME_RETIRED` refuses to start with a leftover identity or without secretd, and seals crash-capture keys to the recipient; fleet-wide `SEAL_DOMAIN_RUNTIME_RETIRED_ON_COMPUTE` stops CD handing compute the key, so joining nodes never get it. Until 3d-3, park snapshots in the shared store still hold plaintext process memory | 3d (except 3d-3) |
| 3f | Log drain headers by reference: secretd `OpenLogDrainHeader` (edge identity on the control plane with `SEAL_DOMAIN_EDGE_ON_CONTROL_PLANE`), gatewayd-internal `FAAS_GATEWAY_LOG_DRAIN_OPENER=secretd` per node (`faas_gateway_secretd_log_drains`) (code in #7003) | 3c |
| 3g-1 | Broker trigger credentials by reference (secretd `OpenTriggerConfig` with the CP's delivery identity); compute schedd `FAAS_SCHEDD_DELIVERY_OPENER=secretd` (`faas_schedd_secretd_delivery`) also leaves webhook delivery to the CP schedd, since account webhooks have no app to authorize by, and refuses execution dispatch and the commit relay (not deployed) (code in #7003) | 3f |
| 3h | Verify-only basic auth: `app_basic_auth_verifiers` (argon2id, keyed by the sealed blob's SHA-256 so every copy path keeps working and a stale verifier cannot match a new credential; apid-only writes), gateway `FAAS_GATEWAY_BASIC_AUTH=sealed|verifier|verifier-only`, backfill in `seal-domain migrate --domain edge` (code in #7003) | 3f |
| 3i | Edge-rule headers injected on the instance's node: the forward init frames (`ForwardHTTPRequestInit`, `ForwardRawRequestInit`) gain `edge_secret_headers` (name, set/add, rule app, row id; proto fields, so neither a client nor a tenant app can forge one); gatewayd-internal `FAAS_GATEWAY_EDGE_HEADER_SECRETS=inject` keeps the reference and never opens the value (a set op still drops the client's own value at the edge, and the shared cache treats the request as credentialed); vmmd `FAAS_VMMD_EDGE_HEADER_SECRETS=secretd` opens each through secretd `OpenEdgeHeaderSecret(target app = the live instance's app, rule app, id)`, cached 5 minutes per target app, adds it after the request's headers (raw upgrades: into the request head), and refuses the forward if any value is unavailable. secretd answers the owner or a granted peer of the target app, for a value of an app with the same resource account that one of its headers rules still references; the audit names the rule's app (code in #7003). Ordering: a secret op now applies after the rule's other ops, and the gateway's own checks no longer see the value | 3h |
| 3j-1 | Registry byte proxy on the control plane (owner decision 2026-11-12: every private pull streams through the CP, not a token broker): secretd `RegistryFetch(app, GET/HEAD, https://<registry>/v2/...)` streams the registry's answer; secretd opens the app's password with the build identity (`FAAS_SECRETD_BUILD_IDENTITY`, staged on the CP with `SEAL_DOMAIN_BUILD_ON_CONTROL_PLANE`), answers Bearer and Basic challenges itself (tokens cached per app and repository), follows up to 5 redirects with the credential sent only to the registry's own `/v2/` API, and dials only public addresses (`oci.NewEgressHTTPClient`). Only the app's own credential host, only `Accept`/`Range`/conditional request headers, only content headers back; at most 64 fetches at once; owner or grant authorizes; each manifest read is audited (code in #7003) | 3h |
| 3j-2 | imaged pulls through `RegistryFetch` (code in #7003): `oci.BasicAuth{Transport}` routes every request of a pull through `pkg/registryproxy` (GET/HEAD only, no credential, blobs kept in the scoped cache), and imaged `FAAS_IMAGED_REGISTRY_PULLS=secretd` (`faas_imaged_secretd_registry_pulls`) hands that out wherever the app holds a credential for the host, reading only that the row exists. secretd also admits the node holding an unfinished `deployment_image_preparations` row of one of the app's deployments (updated within 6 hours), recorded as a grant, and exchanges ECR IAM keys for registry tokens itself. 3j-2b: `RegistryFetch` also takes a `job_id`, pulls with `job_registry_credentials` for the node holding the job's live materialization lease (`jobs.image_materialization_lease_owner` = `<node>/<claim>`), and `secret_open_audit` records it under a new `job_id` (exactly one of app and job) | 3j-1 |
| 3j-3 | Builder VMs pull through a registry mirror vmmd serves on the build netns' host side, backed by `RegistryFetch`; vmmd stops writing a Docker config with passwords; then the build identity leaves compute | 3j-2 |
| 3g-2 | Delivery identity retired from compute (decided above) | 3f |
| 4 | HSM hardware, ceremony runbook, Tier A wrapped identities for every control-plane reader, measurements | — |
| 5 | Tier B envelope v3 + re-seal of every sealed column | 4 |

## Consequences

- A wake that needs secrets depends on secretd on the control plane.
  secretd runs on both control-plane servers; a wake that cannot reach
  either fails closed, with `FaasSecretdDown` paging. Already-running
  instances are unaffected.
- Compute nodes stop being a place where customer secrets can be stolen in
  bulk. A node compromise becomes "rotate the secrets of the apps placed on
  that node during the window", which `secret_open_audit` lists exactly.
  Today it means "rotate every secret of every customer".
- CI compromise stops reaching customer secrets once slice 2 lands.
- HSM hardware, a ceremony and two-operator procedures become part of
  operating the control plane.
- Residual risk, stated plainly: root on a control-plane host during the
  compromise can ask the HSM to open values (Tier B) or read unwrapped
  identities from memory (Tier A only). Detection (Tetragon, HSM audit,
  secretd budgets) and the absence of exportable keys limit this. It does
  not reach zero.
