# Disk encryption: Tang unlock, recovery and rebind

ADR-20261109120000. With a manifest `disk_encryption` section, each host's
`/srv/fc` volume is LUKS2 (`<label>-crypt`, label `faas-fc` by default). It
unlocks at boot through the **other** hosts' Tang servers on the private
network. A recovery key sealed to the operators' age recipients sits at
`/etc/faas/disk-recovery/faas-fc.key.age`.

## Turning it on

1. Make an age identity per operator **off the fleet** (`age-keygen -o
   ~/.config/gregale/disk-recovery.key`), and put the public keys (`age1…`)
   in the manifest:

   ```yaml
   disk_encryption:
     recovery_recipients: ["age1…", "age1…"]
   ```

2. Render the inventory (`make manifest-ansible MANIFEST=…`) and bring Tang
   up on every host first, so new volumes have peers to bind to:

   ```
   ansible-playbook -i <inventory> deploy/ansible/bootstrap.yml --tags tang
   ```

3. Only volumes the storage role creates from now on are encrypted. To move
   an existing host: drain it, rebuild it on blank disks, and rejoin with
   `--format-storage`. Snapshots and layers repopulate.

Check a host:

```
sudo cryptsetup status faas-fc-crypt          # active, type LUKS2
sudo clevis luks list -d <device>             # 1: sss '{"t":1,"pins":{"tang":[…]}}'
findmnt /srv/fc                                # /dev/mapper/faas-fc-crypt xfs
systemctl show faas-vmmd -p RequiresMountsFor  # includes /srv/fc
```

## What lives on the volume

Besides `/srv/fc`'s own data, these paths are bind mounts of
`/srv/fc/host-private/<name>` (Amendments 1 to 3):

| Path | Hosts |
|---|---|
| `/etc/faas/secrets` | every host |
| `/var/lib/faas` (daemon state, HMAC keys, certs, zswap swapfile) | every host |
| `/var/lib/postgresql` (cluster) | control plane, warm standby |
| `/var/lib/pgsql` (WAL archive, base backups) | control plane, warm standby |
| `/srv/db` (customer database volumes, archive) | database nodes |

Control-plane and compute hosts run Tang. Database nodes and the warm standby
only unlock from them.

`findmnt /etc/faas/secrets` shows `/dev/mapper/faas-fc-crypt[/host-private/secrets]`.
The directory underneath each is empty and immutable (`lsattr -d` shows `i`),
so nothing can write secrets or a database to the plain disk while the
volume is locked.

## A host came up with /srv/fc not mounted

Every faas unit requires the mount, so the daemons stay stopped and the host
stays reachable over SSH. Usually no Tang peer answered at boot.

1. Check the peers from the host:
   `curl -fsS http://<peer private address>:7500/adv`, for each peer in
   `clevis luks list`.
2. If one answers, unlock and start:

   ```
   sudo clevis luks unlock -d <device> -n faas-fc-crypt
   sudo mount /srv/fc
   sudo systemctl start local-fs.target remote-fs.target   # the bind mounts, parents first
   sudo systemctl start postgresql  # control plane
   sudo systemctl start --all 'faas-*.service'
   ```

3. If none answers and the peers cannot come back soon, use the recovery
   key (below).

The storage role refuses to run on a host in this state ("refuse while the
encrypted volume is locked"); unlock first, then converge.

## Opening a volume with the recovery key

On an operator machine that holds an identity:

```
scp <host>:/etc/faas/disk-recovery/faas-fc.key.age .
age -d -i ~/.config/gregale/disk-recovery.key faas-fc.key.age > faas-fc.key
scp faas-fc.key <host>:/run/faas-fc.key      # /run is tmpfs
shred -u faas-fc.key
```

On the host:

```
sudo cryptsetup open --key-file /run/faas-fc.key <device> faas-fc-crypt
sudo shred -u /run/faas-fc.key
sudo mount /srv/fc
```

Then rebind, so the next boot unlocks through Tang again.

## Rebinding to the current peers

After hosts join or leave, or when a peer was down at format time (the role
prints "Binding to … only"), rebind the volume to the live peers. Keep the
old binding until the new one is proven:

```
policy='{"t":1,"pins":{"tang":[{"url":"http://10.20.0.1:7500"},{"url":"http://10.20.0.3:7500"}]}}'
sudo clevis luks list -d <device>                      # note the old slot, e.g. 1
sudo clevis luks bind -d <device> -k /run/faas-fc.key sss "$policy"
                                   # the decrypted recovery key (above); shred it after
sudo clevis luks list -d <device>                      # new slot present
sudo clevis luks unlock -d <device> -n faas-fc-test && sudo cryptsetup close faas-fc-test
sudo clevis luks unbind -d <device> -s <old slot>
```

Never bind a host to its own Tang server.

## Rotating a host's Tang keys

Tang keys only advertise public material; rotation retires a key that may
have been copied from a host's root disk. On the host:

```
sudo /usr/libexec/tangd-rotate-keys -d /var/lib/tang   # old keys become .-hidden
```

Then on every **other** host, regenerate its binding (`sudo clevis luks
regen -d <device> -s <slot>`) and, once all have, delete the hidden old keys
on the rotated host. A host whose root disk was compromised is treated as
lost: rotate its Tang keys on a fresh install, and rebind every other host.

## Promoting the standby without the fleet

The standby unlocks from the fleet's Tang servers. If the fleet is gone and
the standby has rebooted, `/srv/fc` stays locked: open it with the recovery
key (above) and `sudo systemctl start local-fs.target remote-fs.target`
before following `docs/runbooks/FaasPostgresStandby.md`.

## Moving a live Postgres

The storage role never stops a control-plane Postgres that holds data; it
stops with "refuse to move a Postgres that holds data". In a maintenance
window (the API is down while Postgres is):

1. Confirm the off-host WAL push is current
   (`/var/lib/pgsql/archive/.offhost-last-success` within a minute) and take a
   base backup (`sudo systemctl start faas-pg-basebackup.service`).
2. Stop the database clients, then Postgres: `sudo systemctl stop
   'faas-*.service' pgbouncer postgresql`.
3. Converge the host (`bootstrap.yml --limit <host>`). With Postgres stopped
   the role still sees the databases and refuses, so move the cluster by hand
   exactly as the role does, for each of `postgresql` and `pgsql`:

   ```
   sudo install -d -m 0700 /srv/fc/host-private
   sudo install -d /srv/fc/host-private/postgresql
   sudo chown --reference=/var/lib/postgresql /srv/fc/host-private/postgresql
   sudo chmod --reference=/var/lib/postgresql /srv/fc/host-private/postgresql
   sudo rsync -aHAX --numeric-ids /var/lib/postgresql/ /srv/fc/host-private/postgresql/
   sudo mount --bind /srv/fc/host-private/postgresql /var/lib/postgresql
   ```

   then converge again: the role adds the fstab entries, proves the copy,
   shreds the originals and seals the directories.
4. Start Postgres and the daemons, and check `findmnt /var/lib/postgresql`.

## Moving /var/lib/faas on a running host

It moves only while no `faas-*` service runs; the role otherwise stops with
"refuse to move daemon state while faas services run". New hosts never hit
this. For a host encrypted before Amendment 2: drain it (compute) or open a
maintenance window (control plane), `sudo systemctl stop 'faas-*.service'`
(the zswap swapfile is swapped off with `faas-zswap`), converge, then start
them again. The OCI cache and Grype mounts beneath it stay on `/srv/fc` and
are put back on top automatically.

## Undoing a sealed directory

To put a path back on the plain disk (only when turning disk encryption
off), reach the directory underneath and clear the flag:

```
sudo mkdir /run/underlay && sudo mount --bind / /run/underlay
sudo chattr -i /run/underlay/etc/faas/secrets
sudo umount /run/underlay && sudo rmdir /run/underlay
```

## A format attempt failed halfway

The role formats only a blank volume. If `luksFormat` succeeded but the bind
or the recovery seal did not, the volume is LUKS with a key nobody holds,
and the role stops at "require the Tang binding". It holds no data: wipe it
(`sudo wipefs -a <device>`) and converge again.

## Losing everything

If every Tang server's keys are gone **and** no operator identity can open the
recovery file, the volume is lost. For `/srv/fc` that means rebuilding the
host; snapshots are cache (ADR-005). Host secrets come back from CD, and the
control plane's database from the off-host WAL archive and base backups
(ADR-1344, `docs/runbooks/ControlPlaneStateBackup.md`). Keep at least two operator identities in
separate places.
