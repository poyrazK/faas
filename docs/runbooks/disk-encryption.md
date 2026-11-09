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

## A format attempt failed halfway

The role formats only a blank volume. If `luksFormat` succeeded but the bind
or the recovery seal did not, the volume is LUKS with a key nobody holds,
and the role stops at "require the Tang binding". It holds no data: wipe it
(`sudo wipefs -a <device>`) and converge again.

## Losing everything

If every Tang server's keys are gone **and** no operator identity can open the
recovery file, the volume is lost. For `/srv/fc` that means rebuilding the
host; snapshots are cache (ADR-005). Keep at least two operator identities in
separate places.
