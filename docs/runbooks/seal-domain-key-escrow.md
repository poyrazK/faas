# Runbook: move private identities out of GitHub into the control-plane key escrow

ADR-20261110210000 §2. Until this runbook is done, the GitHub environment
secrets `FLEET_AGE_KEY` and `SEAL_DOMAIN_<DOMAIN>_AGE_KEY` hold every private
identity that opens customer secrets, and any production CD job receives
them. Afterwards GitHub holds only the public recipients
(`FLEET_AGE_RECIPIENT`, `SEAL_DOMAIN_<DOMAIN>_AGE_RECIPIENT`), and CD reads the
identities from `/etc/faas/secrets/key-escrow/` on the control-plane host
(`CP_HOST`) over SSH pinned to `COMPUTE_KNOWN_HOSTS`.

The escrow is a `0700 root:root` directory of `0400` files named
`<name>.age` (`fleet`, `outbound`, `source`, `alerting`, `storage`,
`control`, `delivery`, `account`, `runtime`, `build`, `edge`, `edge_tls`).
No daemon loads it. It is the key store `secretd` will use (slice 3) and the
one the HSM wraps (slice 4).

Do each environment separately: `production`, then `production-us`.

## 1. Escrow the current identities

1. Run **Actions → Seal-domain key escrow** from `main`, environment
   `<env>`, mode `status`. Every name shows `missing`.
2. Run it again with mode `put`. Each name whose secret is set prints
   `escrowed` (or `unchanged` on a re-run); unset ones print `skipped`. A
   name that prints an error already holds a *different* identity in the
   escrow: stop and find out why before continuing, because `put` never
   replaces one.
3. Run mode `status` again and check that every domain the environment uses
   shows `present`.

Nothing is printed except names and states.

## 2. Switch CD to the escrow

1. In the GitHub environment `<env>`, add the **variable** (not secret)
   `SEAL_DOMAIN_KEY_SOURCE` = `escrow`.
2. **Delete** the secrets `FLEET_AGE_KEY` and every
   `SEAL_DOMAIN_<DOMAIN>_AGE_KEY`. Keep the `_RECIPIENT` secrets.
   CD refuses to run in escrow mode while any of them is still set.
3. Run a normal platform rollout of a release that contains
   `scripts/ci/key_escrow.sh`. An older release fails with "predates the key
   escrow"; that is expected, since it can only read the deleted secrets.
   - The control-plane converge and the compute joins fetch only the
     identities their role needs, use them for that step, and delete them.
   - Each join still proves the staged identities open their domain probes
     (`fleet-seal verify`) before the node is activated, so a wrong escrow
     file fails the join instead of a wake.

Rollback: set `SEAL_DOMAIN_KEY_SOURCE` back to `secrets` **and** restore the
deleted secrets from the escrow. That needs root on the control plane, so
prefer fixing forward.

## 3. Rotate everything GitHub held

Before 2026-11-10 any branch could request the production environments
(ADR-20261110210000 §1), and every production job received these
identities. Treat them as exposed and rotate each domain, one at a time,
only after step 2. Never rotate through GitHub secrets: that would put the
new identity where the old one leaked.

Per domain:

1. **Rotate.** Actions → Seal-domain key escrow, mode `rotate`, domain `<d>`.
   On the control plane, `gregalectl seal-domain init` generates the new
   identity straight into the escrow; the old one is kept as
   `<d>.age.previous`. The run prints only the new public recipient.
   It refuses while an earlier rotation of the domain is still unretired.
2. **Publish the recipient.** Set the secret
   `SEAL_DOMAIN_<D>_AGE_RECIPIENT` to the printed value.
3. **Roll out.** The next platform rollout stages both identities on every
   reader (the role's `faas_seal_domain_<d>_previous_identity_src`), so
   values sealed to either open, and writers seal to the new recipient.
4. **Re-seal** every value of the domain to the new identity:
   `sudo gregalectl seal-domain migrate --domain <d> ...` on a host of the
   domain's reader, as in `docs/runbooks/seal-domains-rollout.md` (it lists
   the host and the `--db-env` per domain), then
   `gregalectl seal-domain verify`.
5. **Retire.** Mode `retire`, domain `<d>`, then roll out again: readers drop
   the old identity.

The fleet identity is not rotated. It is retired
(`faas_compute_fleet_key_retired`, then the control-plane contract).

`gregalectl` names the edge-tls domain `edge-tls`; the escrow, CD and the
workflow name it `edge_tls`. `key_escrow.sh` maps between them.

## Checks

- `Actions → Seal-domain key escrow`, mode `status`: what the escrow holds.
- The environment has no secret whose name ends in `_AGE_KEY`.
- A CD run's log shows `key_escrow` fetches and no "predates" error.
