# secretd: the control plane's secret opener

ADR-20261110210000 §3. `secretd` opens customer runtime secrets for the
compute node that runs the app (the owner, or a peer the owner granted), so
compute nodes can stop holding the runtime seal domain's identity.

## Turn it on (control plane)

1. In the GitHub environment (`production`, `production-us`), set the
   variable `SEAL_DOMAIN_RUNTIME_ON_CONTROL_PLANE` to `true`. The next
   control-plane converge stages `/etc/faas/secrets/domains/runtime.age`
   (0400 root) on the control plane. With `SEAL_DOMAIN_KEY_SOURCE=escrow`,
   CD then fetches the runtime identity from the escrow for the control
   plane too.
2. Set `faas_secretd_listen: "tcp://<control plane private address>:8098"` in
   the control plane's Ansible variables and converge. The role creates the
   `faas-secretd` user, issues the `secretd.faas` leaf and installs the unit
   and its drop-in. The release activation starts it.
3. Check: `systemctl status faas-secretd`, and
   `journalctl -u faas-secretd` shows `secretd: serving` with
   `identities=1` (2 during a rotation).

## Staged PostgreSQL credential verification

Managed PostgreSQL cutover and restore rollback verify staged credentials
through `VerifyPostgresCutover`. The dedicated `apid-postgres-verifier.faas`
client can request a read-only SQL probe for a live leased cutover; it cannot
open application secrets or receive a connection string. secretd checks the
stored target, envelope digest, account binding and fingerprint, then records
`secret_verification_audit` before opening the envelope.

Roll out in this order:

1. Apply migration `20261114190000002_secret_verification_audit.sql` and the
   runtime-role grants before activating the new verifier.
2. Deploy secretd and converge `secretd_service`. It issues both the server
   leaf and `/etc/faas/tls/secretd-verifier/apid.{crt,key}`, and loads the
   control plane's `/etc/faas/secrets/host.hmac.key` with
   `FAAS_SECRETD_POSTGRES_HMAC_KEY`. This must be the same fingerprint key
   used by the local apid; do not route these probes to a different host
   with a different fingerprint key.
3. Deploy apid with the role's `51-faas-postgres-verifier.conf` drop-in. It
   sets `FAAS_APID_SECRETD_TARGET` and its three `FAAS_APID_SECRETD_TLS_*_PATH`
   variables. apid retains local verification for historical fleet-encrypted
   envelopes and delegates runtime-domain envelopes to secretd.
4. Verify a temporary cutover and restore rollback. Successful probes add
   `secret_verification_audit` rows and increment
   `faas_secretd_opens_total{rpc="VerifyPostgresCutover",via="verification"}`.
   Refusals increment `faas_secretd_refusals_total` for that RPC. Missing TLS,
   key, audit access or an expired lease fails verification before promotion.

Keep the audit migration when rolling binaries back. Removing the apid
drop-in restores the historical fleet-only verification path; runtime-domain
cutovers require the new verifier. This change does not stage the private
runtime identity in apid.

## Turn it on for a compute node (slice 3c-1)

Prerequisite: `faas_seal_domain_runtime_binding_required: true` on compute
(every stored runtime envelope is bound; secretd refuses unbound ones).

A peer can open an app's secrets only if the app's owner granted it, so
grants go on everywhere before any vmmd asks secretd:

1. **Grants (every compute node).** Set
   `faas_secretd_target: "tcp://<control plane private address>:8098"` for
   all compute nodes and converge. vmmd_service issues each node's
   `secret-opener` client leaf on the control plane; compute_only_service
   gives schedd `FAAS_SCHEDD_SECRETD_TARGET`. Nothing opens differently yet.
   Check: each schedd's journal shows `schedd: peers granted app secrets
   through secretd`; `secret_grants` gains rows as apps run on peers, and
   `sched: secret grant refused or failed` stays absent.
2. **Opening (one node at a time).** Set `faas_vmmd_secretd_open: true` for
   one node and converge it (`--limit`). vmmd restarts with
   `FAAS_RUNTIME_SECRET_OPENER=secretd`. Check: vmmd's journal shows `vmmd:
   app secrets opened by secretd`, wakes there add `secret_open_audit` rows
   (`via = owner`, `grant` for peer placements), and no wake fails with
   PermissionDenied. Then the next node.

Back out a node by setting `faas_vmmd_secretd_open: false` and converging:
vmmd opens with its own runtime identity again (until step 3 retires it).

## Take the runtime identity off compute (slice 3e)

After step 2 has run on a node for a while with no refusals:

3. **Retire (one node at a time).** Prerequisites on the node:
   `faas_seal_domain_runtime_contracted: true` (the fleet identity also
   opens runtime secrets) and `faas_vmmd_secretd_open: true`. Set
   `faas_seal_domain_runtime_retired_on_compute: true` for it and converge
   (`--limit`). The seal_domains role removes `runtime.age` and
   `runtime.age.previous`, stages the public `runtime.age.pub`, stops
   publishing the runtime probe from the node, and gives vmmd
   `FAAS_SEAL_DOMAIN_RUNTIME_RETIRED=true`. vmmd still holds the identity it
   loaded, so **drain the node and restart vmmd**. Check: vmmd's journal shows
   `vmmd: runtime identity retired`; vmmd refuses to start while either
   file is back on disk; crash captures taken there open (their keys are
   sealed to the runtime recipient, which only secretd opens).
4. **Fleet-wide (CD).** Once every compute node is retired, set the GitHub
   environment variable `SEAL_DOMAIN_RUNTIME_RETIRED_ON_COMPUTE=true` (it
   needs `SEAL_DOMAIN_RUNTIME_ON_CONTROL_PLANE=true`). cd-compute is then not
   handed the runtime identity at all, does not fetch it from the escrow,
   stages only the recipient, and marks every node, including nodes that
   join later, as retired. A compute host now holds no key that opens a
   tenant's runtime secrets.

Back out a node by setting the switch to false and converging: the role
stages the identity again and vmmd opens with it after a restart. With step 4
on, unset the GitHub variable first.

## Log drain headers through secretd (slice 3f)

Drains ship only from the app's owner node, so secretd opens a drain's
authentication header for that node (or a granted peer) by reference.

1. **Edge identity on the control plane.** Set the GitHub environment
   variable `SEAL_DOMAIN_EDGE_ON_CONTROL_PLANE=true` and converge the control
   plane: the seal_domains role installs `edge.age` there and secretd
   restarts with `FAAS_SECRETD_EDGE_IDENTITY`. Check: secretd's journal shows
   `edge_identities=1`.
2. **One node at a time.** With `faas_secretd_target` already set, set
   `faas_gateway_secretd_log_drains: true` for a node and converge it.
   gatewayd-internal restarts with `FAAS_GATEWAY_LOG_DRAIN_OPENER=secretd`.
   Check: its journal shows `log drain headers opened by secretd`, drains
   with a header keep delivering (drain health), and
   `faas_secretd_opens_total{rpc="OpenLogDrainHeader"}` rises as drain
   workers start.

Back out a node with `faas_gateway_secretd_log_drains: false`. The edge
identity stays on compute until basic auth (3h) and edge-rule headers (3i)
move too.

## Delivery values through secretd (slice 3g)

secretd opens broker trigger credentials (Kafka SASL passwords and TLS
keys, Redis Streams passwords, NATS URLs) by reference for the app's owner
node; it reads the delivery identity the control plane already holds for its
schedd. Webhook signing secrets are not opened on compute at all in this
mode: account webhooks have no app to authorize by, so the control plane's
schedd delivers every webhook.

1. Converge the control plane: secretd restarts with
   `FAAS_SECRETD_DELIVERY_IDENTITY`. Check: `delivery_identities=1` in its
   journal.
2. One compute node at a time, with `faas_secretd_target` set: set
   `faas_schedd_secretd_delivery: true` and converge it. schedd restarts with
   `FAAS_SCHEDD_DELIVERY_OPENER=secretd`. Check: its journal shows `webhook
   delivery left to the control plane`, broker triggers keep consuming, and
   `faas_secretd_opens_total{rpc="OpenTriggerConfig"}` rises as pollers are
   built. Execution dispatch and the commit relay refuse to start in this
   mode.

The delivery identity stays on compute until every node runs this way and it
is retired like the runtime identity.

## Verify-only basic auth (slice 3h)

Any node's edge checks basic auth for any app, so the edge stops opening the
credential. apid stores an argon2id verifier for every credential it seals
(`app_basic_auth_verifiers`, keyed by the SHA-256 of the sealed blob, written
only by apid), and the edge checks presented credentials against it.

1. **Deploy** the release with migration `20261111193000292`. New and
   changed credentials get verifiers from then on.
2. **Backfill** the rest where the edge identity is staged:
   `gregalectl seal-domain migrate --domain edge --db-env <owner env>`. Its
   report's `basic_auth_verifiers` counts `written` and `failed`; rerun until
   `failed` is 0 (a failed blob does not open with the staged identities).
3. **Verifier with fallback, fleet-wide:** set
   `faas_gateway_basic_auth: verifier` and converge compute. Check
   `gateway_basic_auth_verifier_missing_total` stays flat.
4. **Verifier only:** set `faas_gateway_basic_auth: verifier-only`. The edge
   never opens a basic-auth credential; one with no verifier refuses every
   password until step 2 is rerun.

An edge-key rotation re-seals credentials into new blobs; the same migrate
command writes their verifiers before it returns.

## Edge-rule request headers on the instance's node (slice 3i)

A headers rule's sealed request header value (`edge_rule_header_secrets`) is
added by the vmmd of the node that runs the instance, which opens it through
secretd. The edge never holds it.

1. **Edge identity on the control plane:** as for slice 3f
   (`SEAL_DOMAIN_EDGE_ON_CONTROL_PLANE=true`), so secretd answers
   `OpenEdgeHeaderSecret`.
2. **Every compute node's vmmd:** set `faas_vmmd_edge_header_secrets: true`
   (needs `faas_secretd_target`) and converge every compute node. Until a
   gateway sends references nothing changes.
3. **Gateways, one node first:** set `faas_gateway_edge_header_secrets:
   inject` on one node and converge it. Check
   `faas_secretd_opens_total{rpc="OpenEdgeHeaderSecret"}` rises when such a
   rule is hit and that requests through that node still reach the app with
   the header. Then the rest of compute.

Do not set inject while any node's vmmd lacks step 2: an older vmmd ignores
the references and the request reaches the app without the header; a
current vmmd without the opener refuses the request (503 at the edge).
`OpenEdgeHeaderSecret` refusals with reason `wrong_account` page through
`FaasSecretdCrossTenantRequest`. Roll back with
`faas_gateway_edge_header_secrets: local` while the edge identity is still on
compute.

## Private-registry pulls through the control plane (slice 3j)

secretd pulls from an app's private registry for the node (and later the
builder VM) that needs the image, so the password and the tokens it buys stay
on the control plane. Every byte of a private image crosses the control
plane: watch its network and `faas_secretd_opens_total{rpc="RegistryFetch"}`.

1. **Build identity on the control plane:** set the GitHub variable
   `SEAL_DOMAIN_BUILD_ON_CONTROL_PLANE=true` and run CD for the control plane;
   `journalctl -u faas-secretd` reports `build_identities` 1 (2 during a
   rotation).
2. **imaged, one node first:** set `faas_imaged_secretd_registry_pulls: true`
   on one compute node (needs `faas_secretd_target`) and converge it. Deploy an
   app with a private image there and check
   `faas_secretd_opens_total{rpc="RegistryFetch"}` rises and the deploy
   succeeds. `faas_secretd_refusals_total{rpc="RegistryFetch",reason="not_authorized"}`
   rising means a node pulled for an app it neither owns, holds a grant for,
   nor prepares an image of: find the path in imaged's journal before
   turning on more nodes. Then the rest of compute.
3. **Builder VMs (slice 3j-3), one node first:** set
   `faas_vmmd_build_registry_mirror: true` on one compute node (needs
   `faas_secretd_target`) and converge it; vmmd logs `builder VMs pull private
   registries through secretd`. A Dockerfile build on that node whose app has
   a registry credential now gets no Docker config: guest-init forwards
   `127.0.0.1:15000+i` to vmmd on vsock, BuildKit lists those as the
   registries' mirrors, and vmmd pulls each request through secretd by build
   id. secretd admits only the node that claimed the running build
   (`builds.builder_node_name`, recorded at the claim; with per-node database
   logins the claim records it and no other node can change it). Run a build
   `FROM` a private base image and check it succeeds and
   `faas_secretd_opens_total{rpc="RegistryFetch"}` rises; a
   `not_authorized` refusal for a build means the claim recorded no node
   (builderd logs `record the build's node`). Then the rest of compute. Once
   every node runs it, no compute daemon opens a registry password, and the
   build identity can leave compute ("Take the build identity off compute"
   below). Job images pull through secretd with imaged's switch (3j-2b).

   Known limit: secretd finds a credential by the registry URL's exact host,
   so a Docker Hub credential stored as `docker.io` is not used for
   `registry-1.docker.io` pulls (as for imaged's pulls, 3j-2).
4. **Builds pull only what their Dockerfiles name (slice 3j-4).** When apid
   accepts a source deploy it records, in `deployments.build_registry_scope`,
   the image repositories every Dockerfile in the source names (`FROM`,
   `COPY --from`, `RUN --mount from=`, ARG defaults and Compose build args
   expanded). The column never changes, and a per-node compute login cannot
   record one. secretd checks each build pull against it per
   `faas_secretd_build_registry_scope`: `observe` (the default) counts
   `faas_secretd_build_registry_scope_total{result}` and logs `build registry
   pull outside its deployment's scope`, refusing nothing; `enforce` refuses
   (`faas_secretd_refusals_total{reason="out_of_scope"}`). Watch observe for a
   few days: `out_of_scope` means a build pulled a repository its Dockerfiles
   do not name (an ARG the parser could not resolve, a Dockerfile under an
   unusual name); `unrecorded` means a deployment created without apid's
   source path (GitOps candidates, older rows). Turn on enforce once both stay
   at 0 for real builds; a node that claims another app's build then gets at
   most that build's own base images. Back out by setting `observe` again.

## Take the edge and delivery identities off compute

Once slices 3f–3i run on every compute node, nothing on compute needs the
edge or delivery identity. The build identity has its own section below.

**Edge, per node.** Prerequisites: `faas_seal_domain_edge_contracted: true`
(the fleet identity also opens edge values), `faas_gateway_secretd_log_drains:
true`, `faas_gateway_basic_auth: verifier-only` and
`faas_gateway_edge_header_secrets: inject` (after
`faas_vmmd_edge_header_secrets` on every node), and
`SEAL_DOMAIN_EDGE_ON_CONTROL_PLANE=true`, so secretd holds the identity. Set
`faas_seal_domain_edge_retired_on_compute: true` for the node and converge
(`--limit`). The role removes `edge.age` and `edge.age.previous`, stops
publishing the edge probe from the node, and gives gatewayd-internal
`FAAS_SEAL_DOMAIN_EDGE_RETIRED=true`; the restart drops the loaded identity.
Check: gatewayd-internal logs `edge identity retired on this node`, refuses to
start if any prerequisite mode is off or an identity is loaded, and the
converge proves its credentials hold no edge, fleet or host identity.

**Delivery, per node.** Prerequisites: schedd's contract step
(`faas_seal_domain_control_contracted` and
`faas_seal_domain_delivery_contracted`) and `faas_schedd_secretd_delivery:
true`. Set `faas_seal_domain_delivery_retired_on_compute: true` and converge
(`--limit`). The role removes `delivery.age` and its `.previous` twin, skips
the delivery probe and the delivery contract check on the node (the control
plane's converge still runs both), and gives schedd
`FAAS_SEAL_DOMAIN_DELIVERY_RETIRED=true`. Check: schedd logs `delivery
identity retired on this node` and refuses to start without the secretd
opener, with a delivery identity, or with `fleet.age` among its credentials.

**Fleet-wide (CD).** Once every compute node is retired, set the GitHub
environment variables `SEAL_DOMAIN_EDGE_RETIRED_ON_COMPUTE=true` (needs
`SEAL_DOMAIN_EDGE_ON_CONTROL_PLANE=true`) and
`SEAL_DOMAIN_DELIVERY_RETIRED_ON_COMPUTE=true`. cd-compute is then not handed
either identity, does not fetch them from the escrow, and marks every node,
including nodes that join later, as retired. The control plane keeps both.

Back out a node by setting its switch to false and converging: the role
stages the identity again and the daemon restarts with it. With the CD
variable on, unset it first.

## Take the build identity off compute (slice 3g-3)

Once imaged (3j-2) and builder VMs (3j-3) pull private registries through
secretd on every compute node, nothing on compute needs the build identity.

**Per node.** Prerequisites: `faas_seal_domain_build_contracted: true` and
`faas_seal_domain_runtime_contracted: true` (imaged and vmmd without the
fleet and host identities, which also open registry passwords),
`faas_imaged_secretd_registry_pulls: true`,
`faas_vmmd_build_registry_mirror: true`, `faas_secretd_target`, and
`SEAL_DOMAIN_BUILD_ON_CONTROL_PLANE=true`, so secretd holds the identity. Set
`faas_seal_domain_build_retired_on_compute: true` for the node and converge
(`--limit`). The role removes `build.age` and `build.age.previous`, stops
publishing the build probe from the node, gives imaged
`FAAS_SEAL_DOMAIN_BUILD_RETIRED=true` (and restarts it without any build,
fleet or host credential) and gives vmmd the same switch. vmmd still holds
the identity it loaded, so **drain the node and restart vmmd**. Check:
imaged logs `build identity retired on this node` and the converge proves its
credentials hold no build, fleet or host identity; vmmd logs `vmmd: build
identity retired`; either refuses to start if its secretd path is off or a
build identity is back. A private-image deploy and a Dockerfile build `FROM`
a private base image both still succeed on the node.

**Fleet-wide (CD).** Once every compute node is retired, set the GitHub
environment variable `SEAL_DOMAIN_BUILD_RETIRED_ON_COMPUTE=true` (needs
`SEAL_DOMAIN_BUILD_ON_CONTROL_PLANE=true`). cd-compute is then not handed the
build identity, does not fetch it from the escrow, and marks every node,
including nodes that join later, as retired. The control plane keeps it for
secretd.

Back out a node by setting its switch to false and converging: the role
stages the identity again; restart vmmd to load it. With the CD variable on,
unset it first.

## Seal park and warm snapshots at the source (slice 3d-3)

Off by default. With it on, vmmd publishes a park or warm capture only as
age-encrypted `.age` twins and returns the capture key sealed to the runtime
recipient; imaged records it on the snapshot row (`snapshots.sealed_key`,
`sealed_capture_id`); a wake hands it back and vmmd opens it through
secretd's `OpenCaptureKey` (cached per node for 10 minutes). A node's
object-store credentials then neither read another app's snapshot memory
nor rewrite it undetected. A snapshot that cannot be opened cold boots
(ADR-005).

1. **Before.** Every vmmd runs a release with `SnapshotRef.sealed_cache`
   (a vmmd without it treats a sealed snapshot as a crash capture and fails
   the wake instead of cold booting). On a node with the runtime identity
   retired (slice 3e), vmmd must open keys through secretd
   (`faas_vmmd_secretd_open`). Run `make test-metal` and `make leakcheck`
   on a native KVM host, and measure the restore p95 on the reference node
   with sealing on: it must stay under 350 ms.
2. **Turn on.** Set `FAAS_SCHEDD_SEAL_SNAPSHOTS=1` for schedd and restart
   it. Check: schedd logs `schedd: park and warm snapshots sealed at the
   source`; new rows carry a key
   (`select count(*) filter (where sealed_key is not null), count(*) from
   snapshots where not stale and created_at > now() - interval '1 hour'`);
   `faas_secretd_opens_total{rpc="OpenCaptureKey"}` rises with sealed wakes.
   Plaintext rows are recaptured sealed at their next park instead of being
   reused; until then they stay plaintext.
3. **Not covered.** Captures under Application Standard native admission
   (measured) and the execution runtime catalog stay plaintext. Encrypted
   memory does not compress or share pages across snapshots: watch
   `snapshot_fleet_avg_mb`.

Turn it off by unsetting the variable: new captures are plaintext again and
sealed rows still restore while the runtime recipient's identity is served.
## Internal-service tokens from secretd (slice 3k)

Compute schedds normally sign the internal-service tokens they attach for
internal_only apps (ADR-119) with the cluster key, which every compute node
can open. With slice 3k they ask secretd instead, for each app they own or
hold a grant for, and need no cluster key.

1. **Control plane.** The control domain identity must be staged on the
   control plane (`/etc/faas/secrets/domains/control.age`, which the CP
   schedd already reads). Converge secretd_service. The unit gains
   `FAAS_SECRETD_CONTROL_IDENTITY`, and after a restart secretd mints. Check:
   from a compute node, a dispatch to an internal_only app still succeeds
   after step 2, and
   `faas_secretd_opens_total{rpc="MintInternalServiceToken"}` rises.
2. **One compute node.** Set `FAAS_SCHEDD_INTERNAL_SVC_MINTER=secretd` for
   its schedd. It needs `FAAS_SCHEDD_SECRETD_TARGET`. Workflow outbound
   (`FAAS_WORKFLOW_OUTBOUND_ENABLED=1`) refuses to start on such a schedd.
   Restart schedd. Check:
   - The journal shows `schedd: internal-service tokens minted by secretd`.
   - Internal_only cron and trigger dispatches still pass.
   - `faas_secretd_refusals_total{rpc="MintInternalServiceToken"}` stays
     flat. A rise means the node dispatches for an app it neither owns nor
     holds a grant for.
3. **Gateways.** From this release on, both internal-only gates refuse a
   token minted for another app, with audit reason `app_mismatch`. A legacy
   token without an `app_id` passes as before.

Back out a node by unsetting the variable and restarting schedd.

## Rotation

The runtime domain rotates as before (`docs/runbooks/seal-domain-key-escrow.md`).
While the previous identity is staged, the drop-in hands it to secretd as
`faas_seal_domain_runtime_previous`. The converge that stages or removes it
restarts secretd.

## Audit

Every open is a row in `secret_open_audit` (node, app, `owner` or `grant`,
entry count, time), written before any plaintext leaves. Daemons other than
secretd cannot write or delete it.

```sql
SELECT n.name, a.slug, o.via, o.entries, o.opened_at
  FROM secret_open_audit o
  JOIN compute_nodes n ON n.id = o.node_id
  JOIN apps a ON a.id = o.app_id
 WHERE o.opened_at > now() - interval '1 hour'
 ORDER BY o.opened_at DESC;
```

A node that opens more than `FAAS_SECRETD_APPS_PER_NODE_HOUR` (default 1000)
distinct apps in an hour is refused, and secretd logs
`secretd: node over its distinct-app budget`. Treat it as a compromised node:
drain it and rotate the runtime domain.

## Alerts

secretd serves `/metrics` on loopback `faas_secretd_metrics_listen`
(`127.0.0.1:9114`), scraped as job `secretd`:

- `faas_secretd_opens_total{rpc,via}`: opens answered and audited (`via` is
  `owner` or `grant`; `GrantPeer` counts grants recorded).
- `faas_secretd_refusals_total{rpc,reason}`: refusals. Reasons:
  `unauthenticated`, `unknown_node`, `not_authorized`, `budget_exhausted`,
  `unreferenced`, `wrong_account`, `other_app`, `envelope`.

Every refusal is also a `secretd: refused ...` log line naming the node and
app: `journalctl -u faas-secretd --since -1h | grep -E 'refused|budget'`.

| Alert | Means | Do |
|---|---|---|
| `FaasSecretdCrossTenantRequest` (page) | A node sent an envelope bound to another account, or asked for another app's capture key. No release does either. | Compromised node: cordon and drain it, rotate the runtime domain, check `secret_open_audit` for what it opened before. |
| `FaasSecretdBudgetExhausted` (page) | A node passed its distinct-app budget for the hour. | Unless a known mass reschedule explains it, as above. Raise `faas_secretd_apps_per_node_hour` only for a node size that runs more apps. |
| `FaasSecretdUnauthorizedCaller` (warn) | Repeated refusals for a node that neither owns the app nor holds a grant, or names no active node. | Check that the node's schedd grants (its `faas-schedd` journal, lines mentioning secretd) and that the app's owner is where it runs. Persisting with no schedd fault: treat as compromised. |
| `FaasSecretdUnreferencedEntry` (warn) | `faas_secretd_require_references` is on but a node still sends ciphertext. | Upgrade that node, or set the switch back to false and converge. |
| `FaasSecretdDown` (page) | secretd is down; nodes that open through it cannot start apps with secrets. | `systemctl status faas-secretd`, `journalctl -u faas-secretd -n 200`. |

## Turn it off

Set `faas_secretd_listen: ""` and converge: the role removes the drop-in and
stops the unit. Then unset `SEAL_DOMAIN_RUNTIME_ON_CONTROL_PLANE`: the next
converge removes `runtime.age` from the control plane. Do not do this once
compute nodes have stopped holding the runtime identity.
