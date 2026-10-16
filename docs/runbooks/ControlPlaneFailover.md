# Control-plane switchover and failover (ADR-1344 §8)

This runbook moves the control plane to the warm standby. Its PostgreSQL is a
streaming replica of the primary (`postgresql.standby`,
`docs/runbooks/FaasPostgresStandby.md`).

- **Planned switchover:** the primary is healthy, for example during the
  GCP → OVH cutover or a hardware move. No committed data is lost. The API
  is down from step 2 until step 6 (target: under 15 minutes).
- **Unplanned failover:** the primary is gone. You lose whatever the
  standby had not received, which `FaasPostgresStandbyLagHigh` tracks;
  normally that is seconds. Target RTO is 15–30 minutes.

In both cases, warm app traffic keeps flowing through the compute nodes
(ADR-1344 P1 static stability). New wakes, deploys and the API wait for the
new control plane.

The control-plane daemons reach PostgreSQL only through its local socket,
so the daemons always run on whichever host is the primary. Never promote
the database on its own and leave the daemons where they are.

## Before you need it

An unplanned failover cannot copy anything from a dead host, so everything
below must already be on the standby. Check it at every drill.

1. **The replica streams.** `FaasPostgresStandby*` alerts are quiet, and
   `make bootstrap-postgres-standby` passes.
2. **Shareable control-plane files are on the standby** with the same owner
   and mode as on the primary:
   - `/etc/faas/sealed.env`, `compute-db.env`, `storage.env`, `otel.env`,
     `profiling.env`
   - `/etc/faas/secrets/session.key`, `host.hmac.key`, `fleet.age`,
     `fleet.age.pub`, `sign-pub.pem`
   - `/etc/faas/secrets/{schedd,meterd,githubd,gatewayd-internal,pg-basebackup-push,storage-box,object-storage}/…`
     and `/etc/faas/object-storage.json`
   - **`/var/lib/faas/audit-hmac.key` and `recovery-hmac.key`.** Bootstrap
     silently generates new ones when they are missing, and new keys break
     audit joins and every recovery code. Copy them before the first
     control-plane converge on the standby.
   - **`/etc/faas/tls/`**, including `ca/ca.key`. Leaf certificates name
     services (`schedd.faas`, `apid.faas`), not hosts, so they stay valid.
     A new CA would mean re-issuing every node's certificates.
   - `/etc/caddy/tls/cloudflare-origin.{pem,key}`, the Alertmanager
     receiver files, the Grafana admin password, and the Loki credentials.
3. **The standby has its own `/etc/faas/secrets/host.age`** (from
   `gregalectl secrets init`). Never copy the primary's. Before it can take
   over, `gregalectl fleet-seal verify` must pass, so that no Postgres row
   depends on the old host key, and the values sealed outside Postgres
   (`FAAS_DNS_PROVIDER_SEALED`) must be re-sealed for it.
4. **CD can reach the standby.** Its SSH host key is in
   `COMPUTE_KNOWN_HOSTS`, so changing `CP_HOST` is the only edit at
   switchover time.
5. **The control-plane address name has a low TTL.** Fleet hosts resolve it
   privately (`/etc/hosts`, rendered from the manifest). Ansible and CD
   reach it through public DNS (`ansible_host` is the address's host part),
   so its public record must be quick to repoint.

## Planned switchover

1. **Freeze changes.** Pause CD for the control plane, and don't run deploys
   or migrations.
2. **Promote with zero loss:**

   ```bash
   make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini \
     postgres-promote PROMOTE_MODE=planned
   ```

   The playbook does these steps on the primary:
   - checks that the standby is streaming;
   - stops every `faas-*` unit and timer;
   - shuts PostgreSQL down cleanly and masks it, which fences it.

   Then on the standby, it waits until the standby has replayed the primary's
   shutdown checkpoint, promotes it, and confirms it accepts writes.
3. **Update the manifest.** In the `control-plane` fleet entry, replace
   `name` with the standby's name and keep `address` unchanged. Every name
   the fleet uses (the address, `schedd.faas`, `apid.faas`, `githubd.faas`,
   `outboundd.faas`) and the `postgresql.dsn` then point at the new host
   with no other edits. Remove `postgresql.standby`; step 8 re-adds it.
4. **Repoint the address's public DNS record** to the new host, then
   re-render: `make manifest-ansible MANIFEST=…`.
5. **Converge the new control plane:** `make … bootstrap-control-plane`.
   The `postgres` role removes the standby settings and restarts PostgreSQL
   as a primary. The services then start on the release you staged there.
6. **Repoint the compute nodes:**

   ```bash
   make … bootstrap-compute
   ```

   The overlay role rewrites `/etc/hosts`. The compute daemons reconnect
   to the same names, now at the new address, and nothing restarts vmmd.
7. **Move the public origin and CD.** Point the platform hosts' Cloudflare
   origin at the new host, set `CP_HOST`, and resume CD. Then check:
   - `make verify-fleet` passes;
   - the API answers;
   - a wake and a deploy succeed.
8. **Rebuild the old primary as the new standby.** Add it back as
   `postgresql.standby`, converge the control plane, then run:

   ```bash
   ANSIBLE_CONFIG=deploy/ansible/ansible.cfg ansible-playbook \
     -i deploy/ansible/.generated/inventory/hosts.ini \
     deploy/ansible/postgres_standby.yml \
     -e faas_postgres_standby_wipe_primary=<old primary name>
   ```

   The `wipe_primary` override is required because that host still holds a
   primary's data. Seeding wipes it and unmasks PostgreSQL. Before any
   failover in the other direction, reconcile its `/etc/faas` files with the
   new primary's.

## Unplanned failover

1. **Decide.** Confirm that the primary is down and won't come back
   quickly, from the provider console and not only from monitoring. Note
   the standby's last replay time; the promote playbook prints it.
2. **Fence the old primary** so it can never come back as a second primary:
   - power it off from the provider console or API, or
   - remove its network access and stop PostgreSQL there.

   A primary that returns after the promotion takes writes that the new
   primary will never see.
3. **Promote:**

   ```bash
   make … postgres-promote PROMOTE_MODE=failover FENCED_PRIMARY=<old primary name>
   ```

4. **Continue with planned steps 3–7.** In step 5, bootstrap must not need
   anything from the old host. If it stops on a missing file, take it from
   "Before you need it" above, and add that file to the list.
5. **Rebuild the old host as the standby later**, with planned step 8, and
   only after you've checked it: it holds writes the new primary never
   received. If those writes matter, extract them before wiping (export the
   affected rows from a copy of its data directory).

## Drill

Run the planned switchover end to end before launch, and again after
every change to this runbook. Record:
- the API downtime;
- each step's duration;
- any file bootstrap asked for that the "Before you need it" list misses;
- that a commit made just before step 2 is present after the promotion.
