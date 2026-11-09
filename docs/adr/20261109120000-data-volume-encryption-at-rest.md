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
   `/etc/faas/secrets` move onto the encrypted volume in a follow-up slice.
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
