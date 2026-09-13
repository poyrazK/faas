# native_acceptance_host

Provisions a node so the disruptive native CI gates can run on it:
`builder-native.yml` (builder image + the `pkg/fcvm` metal package) and
`e2e-native.yml` (the whole `./cmd/e2e` suite with the `metal` build tag).

Opt-in and never part of `bootstrap.yml` or `node_join.yml`. Run it with the
dedicated playbook:

```sh
ansible-playbook -i inventory/native-acceptance.ini native-acceptance-host.yml
```

## Why this role exists

On 2026-09-12 both native gates were dead on `faas-compute-node-2`, and nothing
reported it as a failure of provisioning:

- `/etc/faas/builder-acceptance-host` was missing, so every runner exited at its
  first guard;
- `make` and `gcc` were not installed, so even with the marker the runners would
  have exited at the tool check;
- there was no Postgres at all, which `cmd/e2e` requires.

All three had been present for the metal suite's run three days earlier. They
were lost in a reprovision because **no role owned them** — they had been placed
by hand. This role is that owner.

## What it does

1. Asserts explicit opt-in, Linux x86_64, and a real `/dev/kvm`.
2. Installs the host tooling the runners resolve from `PATH`, then verifies each
   one with `command -v` rather than trusting the package manager.
   `build-essential` rather than bare `gcc`, because `go test -race` needs cgo
   and therefore libc headers too.
3. Installs a **local, dedicated** PostgreSQL cluster for the e2e gate, bound to
   the unix socket only, with `pg_hba.conf` written wholesale and an ident map
   from `root` to the `faas` database role.
4. Creates the `faas_e2e` database owned by `faas` — owner, not superuser:
   `pgtest` needs `CREATE SCHEMA` and `citext`, and citext has been trusted
   since PostgreSQL 13.
5. Proves the runner's exact DSN can create a schema and install citext.
6. Writes `/etc/faas/e2e-acceptance.env` (root, `0600`) with that DSN.
7. **Last**, writes the designation marker.

## Two ordering and scope decisions worth keeping

**The marker is written last, and only when `nah_designate` is true.** The
runners read it as "this host consents to having its daemons stopped and
microVMs booted", and they check it before touching anything. A half-provisioned
host must never carry it. Set `nah_designate=false` to install the tooling
without authorizing CI yet.

**The e2e cluster is local and separate from the control-plane cluster.** Do not
be tempted to point the gate at `fsn-1`. On 2026-09-12 that cluster hit
`max_connections` with 88 of 96 backends idle, and `cmd/e2e` opens a schema per
test. A test cluster must also not share a failure domain with production rows.

This role does not reuse `roles/postgres`, which sets `wal_level`,
`archive_mode`, an rclone `archive_command` and WAL senders. That machinery
belongs to the control plane; on a throwaway test cluster a failing
`archive_command` would accumulate WAL until the disk filled.

## The one thing it does not provide

`/srv/fc/base/builder-base.ext4`, drive0 for the builder microVM. `faas-imaged`
stages it on startup via `EnsureBaseExt4`, so it appears once that daemon can
reach the control-plane database and start cleanly. `run-native-e2e.sh`
pre-flights it and names the repair.

## Kill switch

Delete `/etc/faas/builder-acceptance-host`. Both runners exit before changing
any service state.
