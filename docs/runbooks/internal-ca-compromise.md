# Internal mTLS CA or leaf compromise

Use this when the fleet's internal PKI may be exposed: the CA private key
(`/etc/faas/tls/ca/ca.key`), a daemon's leaf key, or a bundle that contains
either. Ways that happens: root on the control plane or the standby, a leaked
control-plane state backup together with its operator identity, a leaked
`COMPUTE_PKI_TARBALL_B64` environment secret, or a compute node compromise
(which exposes that node's leaves only; compute nodes never hold `ca.key`).

Internal mTLS is how daemons prove who they are to each other (ADR-052). A
leaked leaf lets the holder impersonate that daemon to its peers. A leaked CA
key lets the holder mint any identity. This is SEV1 with
[breach-notification.md](breach-notification.md) in parallel.

For expiry and renewal failures, use
[FaasInternalMTLSRenewal.md](FaasInternalMTLSRenewal.md) instead.

## What the platform can and cannot do today

- **Leaves can be reissued** through the internal-pki-renewal workflow and
  `gregalectl pki rotate`. A reissued leaf does not invalidate the old one:
  there is no revocation list and no revoke command (ADR-052 deferred
  CRL / OCSP). A stolen leaf stays valid until its `NotAfter`.
- **The CA cannot be rotated online.** The renewal path rejects a different
  CA on purpose, and the trust-root rotation procedure that
  FaasInternalMTLSRenewal.md refers to has not been written. Replacing the CA
  is a planned, fleet-wide change.

So containment relies on the network: internal listeners must only be
reachable from fleet addresses.

## 1. Contain (first 30 minutes)

1. **Close the network path.** Confirm internal daemon ports accept only
   fleet peers: host nftables (`nft list ruleset` on each host) and the
   provider firewall. With the attacker outside the private network, a
   stolen leaf or CA key cannot be presented to a peer. If they are inside
   (root on a fleet host), drain and isolate that host first:

   ```sh
   gregalectl compute-nodes drain --node <fqdn> --reason 'SEV1 <ticket>: PKI exposure'
   ```

2. **Pause releases** if the control plane itself may be compromised (see
   [customer-secret-store-compromise.md](customer-secret-store-compromise.md) §1):

   ```sh
   sudo gregalectl break-glass pause --db-env /etc/faas/compute-db.env \
     --reason "SEV1 <ticket>: PKI exposure"
   ```
3. Preserve `/etc/faas/tls` from the affected hosts before changing anything
   (`cp -a /etc/faas/tls /var/backups/faas-tls-<ticket>` and copy it off the
   host).

## 2. Scope

| Exposed | Impact | Go to |
|---|---|---|
| One compute node's leaves | that node's daemon identities | §3 |
| Control plane or standby root, or a decrypted state backup | the CA key: every identity | §4 |
| `COMPUTE_PKI_TARBALL_B64` | whatever it contains; inspect a copy | §3 or §4 |

Anything that exposed the control plane's root almost certainly exposed more
than the CA. Follow [customer-secret-store-compromise.md](customer-secret-store-compromise.md)
for the sealed identities on the same host.

## 3. Leaf compromise

1. Rebuild the affected node from a trusted release with a new identity
   ([provider-neutral-node-join.md](provider-neutral-node-join.md)). Do not
   reuse its name and leaves.
2. Until the old leaves expire, keep the old node's addresses out of the
   fleet allowlists, so a stolen leaf cannot be used from the old address.
3. Record the old leaves' serials and `NotAfter` (`gregalectl pki list --json`)
   in the incident record. The risk window ends at the latest `NotAfter`.

## 4. CA compromise

There is no tested procedure for this yet. Do not improvise one on a live
fleet in the first hour. Decide with the platform owner between:

- **Contain and plan.** Keep the network path closed (§1), rebuild any
  compromised host, and schedule a fleet-wide CA replacement as planned
  maintenance with a written procedure: new CA, leaves issued from it on
  every host, trust switched in one maintenance window, old CA deleted from
  the standby, the state backup and the GitHub environment.
- **Rebuild the fleet.** If root on the control plane was confirmed, a
  rebuild with new identities may be faster and safer than an in-place
  re-key. The steps are [cloud-provider-loss.md](cloud-provider-loss.md) §4,
  but bootstrap must generate a **new** CA rather than restore the old one.

Either way the old CA key must end up deleted everywhere it was staged: the
control plane, the standby's `/var/lib/faas-standby`, every control-plane
state backup bundle, and any GitHub environment secret.

## 5. Close

Post-mortem required. Track the two missing controls as follow-ups: leaf
revocation, and a rehearsed CA rotation procedure.

## Before you need it

- [ ] Write and rehearse a CA rotation that trusts old and new CAs during
      the switch.
- [ ] Shorten leaf lifetimes, or implement revocation, so a leaked leaf is
      useful for days rather than until a long `NotAfter`.
- [ ] Keep `ca.key` out of GitHub environments.
