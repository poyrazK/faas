# Compute node compromise

Use this when someone may have root, or code execution as a platform user, on
a compute node. Signs include:

- a `FaasHostSecurityCritical` page or a `FaasHostSecurityEvent` nobody can
  explain ([FaasHostSecurityEvent.md](FaasHostSecurityEvent.md));
- a kernel `Lockdown:` refusal on a host where nobody was debugging;
- an unexpected certificate for the apps domains
  ([FaasEdgeCertificate.md](FaasEdgeCertificate.md),
  `FaasEdgeCertificateUnexpected`);
- a honeytoken used ([FaasHoneytokenPresented.md](FaasHoneytokenPresented.md));
- a provider abuse report, or a binary under `/opt/faas` that does not match
  the release.

A guest escaping its microVM is the same incident: treat the node as
compromised.

Severity: SEV1. Compute nodes serve customer traffic directly
(ADR-20261103163954), so assume the attacker can read and change any traffic
that node terminated.

Work from the control plane and the operator workstation. **Do not** log in to
the node to look around, reboot it, or run Ansible against it first:

- A converge hands it fresh copies of every secret you are about to rotate.
- A reboot loses memory and running processes.
- Anything you type there, the attacker can watch.

## 1. Cut it off (first 15 minutes)

The order matters. Every compute node can write the node registry
(ADR-20261107180000), so while the node still reaches Postgres it can undo a
drain. Cut its access first, then drain.

1. **Remove its address from the control plane's allowlists.** Take the
   node's private and overlay addresses out of `faas_postgres_allowed_cidrs`
   (that variable feeds both pg_hba and the nftables role), then converge only
   the control plane's access policy, the same play a node join runs:

   ```sh
   make manifest-ansible MANIFEST=<manifest>   # after removing the node from it
   ansible-playbook -i deploy/ansible/.generated/inventory/hosts.ini \
     deploy/ansible/node_join_control_plane.yml --limit control_plane
   ```

   Then check from the control plane that its connections are gone:

   ```sh
   sudo -u faas psql -Atc "select client_addr, usename, application_name from pg_stat_activity where client_addr = '<node address>'"
   ```

   Close any that remain: `select pg_terminate_backend(pid) ...` for that
   `client_addr`.
2. **Remove it from the overlay.** Tailscale: delete the device in the admin
   console and revoke any auth key it was joined with. WireGuard: remove its
   peer from every other host's config and converge them. This also stops it
   reaching vmmd, schedd and gatewayd-internal on other nodes.
3. **Drain it.** Use force-drain so live instances are cold-evicted and
   restarted elsewhere:

   ```sh
   gregalectl compute-nodes force-drain --node <fqdn> --reason node_compromise --yes
   gregalectl compute-nodes drain-status --node <fqdn>
   ```

   edgednsd withdraws a drained node's address from the apps domains' DNS
   records. Confirm it with `dig +short <app>.<apps domain>` from outside.
   edgednsd never removes the last address and refuses a mass drain; if this
   was the only compute node, put the apps domains behind Cloudflare's proxy
   until a clean node is up.
4. **Isolate the host at the provider.** Leave it powered on for now.
   - GCP: attach a deny-all ingress and egress firewall rule by its network
     tag.
   - Bare metal: the provider's firewall, or null-route its public IP.

   Still leave it powered on: memory and process state are evidence.

## 2. Keep the evidence

1. **Snapshot its disks** before anything else touches them. On GCP:
   `gcloud compute disks snapshot <disk> --zone <zone> --snapshot-names <node>-compromise-<date>`.
   On bare metal, ask the provider for a rescue boot and image the disks
   read-only.
2. **Export the off-host logs.** They left the host as they were written
   (ADR-20261107203000), so they are the record the attacker could not edit:

   ```logql
   {job="faas-security", host="<node>"}
   {host="<node>"} |= "sshd"
   ```

   Export them for the window from the first sign back to the node's last
   clean converge. Also note when Alloy stopped shipping from it
   (`FaasLokiPromtailDown`): the attacker may have stopped it there.
3. Record in the incident log the earliest suspicious event, when access was
   cut, and the node's release version
   (`gregalectl compute-nodes release-status`).

## 3. What the node held

With root, assume the attacker read everything below. Each row says what that
secret reaches beyond this node, and so what to rotate.

| Secret | On the node | Reaches | Rotate |
|---|---|---|---|
| `fleet.age` | `/etc/faas/secrets/fleet.age` | everything still fleet-sealed, on every node (ADR-178) | §4.1 |
| Seal-domain identities `runtime`, `build`, `edge`, `edge-tls` | `/etc/faas/secrets/domains/*.age` (+ `.previous`) | runtime secrets, registry passwords, basic-auth and log-drain credentials, the apps certificate key | §4.1, §4.3 |
| Workload-identity signing key | `/etc/faas/secrets/workload-identity.key` | tokens for **any** app: queue leases, Flags, cloud federation (ADR-1141) | §4.2 |
| Apps-domain certificate key | gatewayd-public memory | impersonating every app until it expires (≤ ~6.7 days, ADR-20261108100000) | §4.3 |
| Database login (`faas_compute`, shared by every compute node) | `/etc/faas/compute-db.env` | the control plane's Postgres, as that role, from any allowed address | §4.4 |
| Object storage provider keys | `/etc/faas/secrets/object-storage/provider.env` | customer buckets through the provider | §4.4 |
| Cloudflare tunnel token | `/etc/faas/secrets/cloudflared/tunnel-token` | running a connector for that tunnel | §4.4 |
| Gateway fleet key (`gatewayd_edge_challenge_key`, shared by every gateway) | `/etc/faas/secrets/gatewayd-internal/edge-challenge.key` | minting browser-challenge passes and managed deadlines that every gateway accepts, which skips challenge rules for any app (ADR-20261102191500) | §4.5 |
| Origin certificate key, when the edge loads its certificate from files | `/etc/faas/secrets/edge-tls/origin.key` | answering as the origin for every app host to Cloudflare, from wherever Cloudflare can be pointed | §4.5 |
| Overlay auth key | `/etc/faas/secrets/tailscale.authkey` | joining the tailnet | §1.2 |
| Its internal mTLS leaf certificates | `/etc/faas/tls/` | talking to other daemons as this node until they expire | [internal-ca-compromise.md](internal-ca-compromise.md) §3 |
| `host.age`, vmmd and builderd `node.key` | `/etc/faas/secrets/` | this node only | retire the node (§5) |
| Customer data on the node | snapshots, layers, guest memory, traffic it terminated | the apps placed there in the window | §6 |

Check the node for anything else staged under `/etc/faas/secrets` on its disk
snapshot, not on the live host.

## 4. Rotate

Do these from the operator workstation, and converge only healthy nodes, so
no new secret reaches the compromised one.

1. **Seal domains and `fleet.age`.** Follow
   [customer-secret-store-compromise.md](customer-secret-store-compromise.md)
   §2–§3 with "a root compromise of this node" as the scope: rotate each
   domain the node staged, re-seal what it opens, and treat everything still
   sealed to `fleet.age` as read. The new identities go only to the other
   nodes. Rotating `edge-tls` also makes edgednsd order a new apps
   certificate (§4.3).
2. **Workload-identity key: rotate twice.** A single
   `gregalectl workload-identity rotate` keeps the previous public key in the
   JWKS so in-flight tokens still verify, and that previous key is the one
   the attacker has. Rotate a second time so the JWKS holds only the two new
   keys:

   ```sh
   gregalectl workload-identity rotate --out <dir>
   gregalectl workload-identity rotate --out <dir>
   ```

   Install the JWKS on the control plane, then the key on the healthy compute
   nodes (FaasWorkloadIdentityNotConfigured.md, Recover). Cloud providers
   that federate with Gregale may cache the old JWKS; check each one's cache
   lifetime in its trust configuration.
3. **Apps certificate.** Once `edge-tls` is rotated, edgednsd orders a
   certificate with a new key (FaasEdgeCertificate.md, "A lost or exposed
   key"). The old certificate stops working within about 6.7 days whatever
   you do. Revoke it anyway: some clients do check.
4. **Shared credentials.** Change `faas_postgres_compute_password` (Ansible
   Vault), converge the control plane's `postgres` role so `faas_compute`
   takes it, and roll the new `compute-db.env` to the healthy nodes. Until
   then the old password works from any address still in the allowlist. Rotate the
   object-storage provider keys and the Cloudflare tunnel token in each
   provider's console. Then confirm the old ones fail.
5. **Edge keys.** Make a new gateway fleet key (`openssl rand -hex 32`), put
   it in Ansible Vault as `gatewayd_edge_challenge_key` and in the
   `GATEWAYD_EDGE_CHALLENGE_KEY` deploy secret, and converge every healthy
   gateway in one run: gateways with different keys refuse each other's
   passes (`FaasEdgeChallengeKeyMismatch`), and visitors behind a challenge
   rule are challenged once more. If the edge loads its certificate from
   files, that key is a copy of the control plane's Caddy origin key
   (EdgeDirectEntry.md): issue a new Cloudflare Origin CA certificate,
   install it in `/etc/caddy/tls/cloudflare-origin.{pem,key}` on the control
   plane and in `/etc/faas/secrets/edge-tls/origin.{pem,key}` on the healthy
   nodes, reload both, and revoke the old one in the Cloudflare dashboard.
6. **Registry rows.** The node could rewrite any `compute_nodes` row. schedd
   and the gateways dial vmmd only when the answering certificate names the
   node they meant (ADR-20261108124106), so a rewritten `target_url` stops
   traffic rather than redirecting it; still restore every row's
   `target_url`, `overlay_ip` and lifecycle from the manifest and the last
   good state before undraining.

## 5. Rebuild, do not clean

1. Once drain-status reports the node empty, retire it:

   ```sh
   gregalectl compute-nodes retire --node <fqdn> --reason node_compromise --yes
   ```

   A retired node is refused for any new admission (ADR-1528).
2. Destroy the machine after the disk snapshot is safe. Do not reuse its
   disks or its `host.age`.
3. Join a replacement under a new name from a fresh image
   ([provider-neutral-node-join.md](provider-neutral-node-join.md)). It gets
   the rotated secrets from §4.
4. Find how the attacker got in before calling this closed: an app escape, an
   exposed service, a stolen operator credential
   ([operator-credential-compromise.md](operator-credential-compromise.md)),
   or the release pipeline
   ([release-pipeline-compromise.md](release-pipeline-compromise.md)). The
   same hole is still open on every other node.

## 6. Customers

1. List the apps placed on the node in the window: the scheduler's instance
   history for that node, plus any app whose traffic it served (every app,
   if it answered for the apps domains).
2. Customers whose secrets were sealed to an exposed identity, or whose
   instances ran there, may need to rotate their own credentials. Tell them
   which ones and why.
3. Personal data in reach means notification duties:
   [breach-notification.md](breach-notification.md).

## 7. Close

- Every secret in §3 is rotated, and its old value fails.
- The node is retired and destroyed, and its replacement is serving.
- The way in is found and closed on every node.
- The evidence is stored with the incident record.
- A drill entry is added in [docs/drills/README.md](../drills/README.md).
