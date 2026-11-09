# ADR-20261109120000: Data volumes are encrypted at rest and unlock through the fleet's Tang servers

- Status: Accepted
- Date: 2026-11-09
- Related: ADR-20261109100000 (bare-metal private network boundary),
  ADR-1344 §8 (OVH move), spec §11 (security rules), gap G2 (sealed secrets)

## Context

Gregale is moving from GCP VMs to OVH bare metal. GCP encrypts every
persistent disk with keys the host never sees; a stolen GCP disk image is
ciphertext to anyone outside the project. OVH gives no such layer. On a
dedicated server, the disks can be read in ways that bypass the running OS:

- the provider's rescue boot, which an operator (or anyone who takes over
  the OVH account) starts from the console;
- a failed drive that the provider swaps and takes away;
- a server returned at the end of a contract.

`/srv/fc` holds customer data on every host: app layers, snapshots (guest
memory images, which can hold anything a customer's process had in memory)
and build artifacts. It must not be readable on a disk that has left the
fleet.

Options weighed:

1. **Passphrase typed at boot** (LUKS + SSH in the initramfs). A reboot needs
   an operator; nodes would stay down until one shows up. Rejected for a fleet
   that reboots for kernel patches.
2. **Key in the TPM.** Ties unlock to the board; protects a pulled drive but
   not the rescue boot on the same machine, which is the main threat. Not
   every OVH range has a usable TPM.
3. **Network-bound unlock (Clevis + Tang)** over the vRack. A volume opens
   only while the host can reach a Tang server on the fleet's private
   network. A disk read in the rescue system, or elsewhere, cannot reach one.

## Decision

1. **Scope: the data volume.** The `storage` role creates `/srv/fc` as LUKS2
   with XFS inside. The root disk stays plain: it holds the OS, packages and
   configuration, which are rebuildable. Postgres data and
   `/etc/faas/secrets` move onto the encrypted volume in a follow-up slice
   (Amendment 1).
2. **Unlock: Clevis bound to the OTHER hosts' Tang servers**, Shamir
   threshold 1, so any one reachable peer unlocks the volume. A host never
   binds to its own Tang server: its Tang keys sit on its plain root disk, and
   whoever holds that disk would hold the means to open the volume beside it.
   Every host runs Tang (`tang_server` role); a fleet therefore needs at least
   two hosts, which manifest validation enforces.
3. **Tang listens on the private address only.** The socket binds the host's
   manifest address (refused if it is not a private address), so Tang is never
   on the public card, even on a fresh host before the nftables role runs.
   nftables then admits the Tang port from `overlay.cidr` on the private card.
4. **Recovery key.** A second LUKS slot holds a random key that is sealed with
   `age` to the operator recipients in the manifest and stored beside the
   volume at `/etc/faas/disk-recovery/<label>.key.age`. It is worthless
   without an identity, which never lives on the fleet. It opens the volume
   if every Tang server is lost.
5. **Boot and fail-closed.** `/etc/crypttab` opens the volume with
   `_netdev` (after the network), `clevis-luks-askpass.path` answers the
   prompt from Tang, and the mount is `_netdev,nofail` so a locked volume
   does not drop the host into emergency mode (it stays reachable over SSH).
   A drop-in for every `faas-*` unit adds `RequiresMountsFor=/srv/fc`, so no
   daemon starts against the empty mountpoint on the root disk. The storage
   role refuses to touch a host whose encrypted volume is locked.
6. **Manifest.** A top-level `disk_encryption` section turns it on:
   `tang_port` (default 7500) and `recovery_recipients` (at least one age
   public key). Absent, nothing changes: no Tang, no rule, plain volumes.
7. **New volumes only.** An existing plain volume is never converted in
   place. A host moves by drain, rebuild on a blank disk, and rejoin; app
   layers and snapshots repopulate (snapshots are cache, ADR-005).

## Consequences

- A disk read outside the fleet (rescue boot, swapped drive, returned server)
  is ciphertext. A host that reboots unlocks by itself as long as one other
  host is up.
- **Whole-fleet cold start:** if every host goes down together, each waits
  for a peer's Tang. Tang starts early from the root disk and needs no
  unlocked volume, so hosts unlock each other as they come up. It needs at
  least two hosts up at once.
- **Threat not covered:** an attacker who controls a running host, or who
  can boot a host's own root disk while the fleet is up and its vRack is
  still attached, can unlock it like the host does. Disk encryption protects
  data on disks that have left the fleet, not a live compromise
  (see `docs/runbooks/compute-node-compromise.md`).
- XTS-AES on modern x86 costs a few percent of throughput with AES-NI;
  snapshot restore reads from page cache-warm files and is not expected to
  move. The wake p95 is to be re-measured on the first encrypted OVH node.
- Operators hold the age identities. Losing all of them and every Tang key
  together loses the data on that volume. Slice 1 starts with `/srv/fc`
  because the cost of that loss is lowest there (snapshots are cache,
  ADR-005); Postgres follows once the recovery path has been exercised.

## Amendment 1 (2026-11-09): host secrets and the control plane's Postgres

Slice 2 moves the remaining sensitive data on a host with an encrypted volume
onto it. Scope chosen: control-plane and compute hosts (the ones with
`/srv/fc`); the Postgres standby host and database nodes get their own
encrypted volume in a later slice.

1. **Paths keep their names.** `/etc/faas/secrets` (every host) and, on the
   control plane, `/var/lib/postgresql` (the cluster) and `/var/lib/pgsql`
   (WAL archive, local base backups) are bind mounts of
   `/srv/fc/host-private/<name>`. About sixty readers of `/etc/faas/secrets`,
   Postgres and its archive command need no change.
2. **Move, prove, shred.** The first converge copies a path's contents onto
   the volume (rsync, ownership, modes, ACLs, xattrs and hard links kept),
   binds it, and compares the hidden original with the copy by checksum. Only
   an identical copy lets the original be shredded. The shred runs through a
   non-recursive bind of the filesystem underneath and refuses unless the
   path is bound from the volume, so it can never reach live data.
3. **Fail closed when locked.** The empty directory under each mount is made
   immutable (`chattr +i`). While the volume is locked the bind mounts are
   absent (`nofail`, `x-systemd.requires-mounts-for=/srv/fc`), and anything
   that would write there instead (a converge that skips the storage role,
   apt's `initdb`, a daemon) fails instead of putting secrets or a database
   on the plain disk. faas units also require `/etc/faas/secrets` and
   `postgresql`/`postgresql@` require both Postgres paths.
4. **Live Postgres is never stopped by a converge.** A cluster on the plain
   disk with more than the template databases makes the role stop with the
   maintenance-window steps (runbook "Moving a live Postgres"). The role
   never stops Postgres: a running fresh cluster needs `systemctl stop
   postgresql` and a second converge. On a new host Postgres is not installed
   when the storage role runs, so neither case arises.
5. **auditd.** Its `faas_secrets` watch is loaded before the mount exists at
   boot; `faas-secrets-audit-reload.service` reloads the rules once
   `/etc/faas/secrets` is mounted.

Consequences: the plain root disk now keeps only the OS, configuration,
Tang's own keys and the network keys needed to reach a Tang peer (WireGuard
or the vRack needs none). `shred` on an SSD is best effort (wear levelling
may keep old blocks); on a fresh host the originals were written minutes
earlier and are few. A volume lost with every Tang key and every recovery
identity now loses the local Postgres cluster too; the off-host WAL archive
and base backups (ADR-1344) remain the way back.

## Amendment 2 (2026-11-09): daemon state in /var/lib/faas

Amendment 1 missed `/var/lib/faas`, which holds secrets of its own
(`audit-hmac.key`, `recovery-hmac.key`, certificates, the `faas_pgproxyd`
password, log-drain settings) and the zswap swapfile, where frozen tenant
VMs' memory is written. It joins `/etc/faas/secrets` on every host with an
encrypted volume, by the same copy, prove, shred and seal.

- It is written continuously, so it moves only while no `faas-*` service is
  active; otherwise the role stops with the drain-and-stop steps. A new host
  has none running when the storage role runs.
- The OCI cache and Grype database are already bind mounts from `/srv/fc`
  beneath it. The copy stays on one filesystem, steps those mounts aside for
  the bind and mounts them again from fstab; the proof leaves out whatever
  lies under them on the boot disk (an OCI cache left there before it moved),
  which is shredded with the rest. A mount beneath it that fstab does not
  list stops the move.
- faas units, `faas-zswap` among them, require the mount.

## Amendment 3 (2026-11-09): the warm standby and database nodes

The two remaining holders of database data get the same encrypted `/srv/fc`.

1. **Warm standby** (ADR-1344 §8). `postgres_standby.yml` runs the storage
   role before PostgreSQL is installed, so the replica, its WAL archive,
   `/etc/faas/secrets` and `/var/lib/faas` (the staged control-plane files)
   live on the volume. It unlocks from every control-plane and compute host's
   Tang server; it serves no Tang itself. A takeover's bootstrap finds the
   volume in place. In a disaster where the fleet is gone, the standby can
   still be opened: the operator promoting it opens its volume with the
   recovery key first (runbook "Opening a volume with the recovery key").
2. **Database nodes** (ADR-1027). `database_node.yml` runs the storage role
   before anything writes to the node (release, PKI, the Firecracker kernel
   in `/srv/fc`, database volumes). `/srv/db`, which holds the customer
   database volumes and their archive, joins the moved paths, quiesced like
   `/var/lib/faas`. Database nodes unlock from the control-plane and compute
   hosts' Tang servers and serve none.
3. **Tang servers** are the control-plane and compute hosts only. Slice 1
   listed database hosts as peers although they run no Tang; the renderer now
   leaves them out, and validation counts at least two Tang servers.
4. **Static overlay only.** The storage role runs before the overlay role on
   a fresh host, and boot-time unlock runs before any overlay service, so Tang
   must be reachable over a provider network that is up by itself.
   `disk_encryption` now requires `overlay.provider: static` (an OVH vRack).

Both apply only when the manifest has `disk_encryption`, and only to new
hosts: an existing standby or database node with data on the plain disk is
refused by the same checks as the control plane (rebuild it on blank disks).
