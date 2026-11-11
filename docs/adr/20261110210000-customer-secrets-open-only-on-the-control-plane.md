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

When a domain has moved, node join stops staging its identity, deletes
leftovers (the `faas_compute_fleet_key_retired` pattern), and
`fleet-seal verify` refuses a compute node that still holds it.

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
| 1c | Placement inputs not compute-writable (per-node DB identity or CP-owned capacity), owner decision | 1a |
| 2 | CI holds recipients only: key escrow on the control plane, `SEAL_DOMAIN_KEY_SOURCE=escrow`, escrow/rotate workflow, runbook (code in this PR); then the owner escrows, deletes the GitHub secrets and rotates | — |
| 3 | `secretd` + `OpenWorkloadSecrets` (owner by protected `apps.node_id`, peers by owner-requested `secret_grants`) + audit + alerts; move `runtime`, then `build`, `edge`, `delivery`; `control` behind signd; node join stops staging, verify refuses leftovers | 1 |
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
