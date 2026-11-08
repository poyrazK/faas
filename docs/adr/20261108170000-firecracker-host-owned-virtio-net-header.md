# ADR-20261108170000: Firecracker writes a host-owned virtio-net header; guests get no offloads

- Status: Proposed
- Date: 2026-11-08
- Related: ADR-983 (Firecracker 1.17), ADR-20261105100000 (resident executables),
  ADR-113 (canonical signed release), ADR-150 (earlier patched-Firecracker canary)

## Context

CVE-2026-99999 ("PwnEscape", CVSS 9.3) is a host kernel bug: before upstream
commit 6785011f8b16 the IPv4 path ran `skb_checksum_help` without checking that
`csum_start`/`csum_offset` point into the transport header. A guest that sets
those fields can make the host's checksum write land in an IPv4 header the host
already parsed, and the published chain turns that into code execution in the
Firecracker process. Every kernel since 2.6.27 is affected.

On 2026-11-08 every production host (`gregale-cp`, `gregale-compute-1`,
`gregale-compute-2`) was booted on `7.0.0-1011-gcp`, and Ubuntu's tracker lists
noble `linux-gcp-7.0` as "vulnerable, work in progress". There is no kernel to
install yet.

The guest controls those fields because of how Firecracker's virtio-net device
works:

- Firecracker v1.17.0 offers `VIRTIO_NET_F_CSUM`, `HOST_TSO4/6` and `HOST_UFO`,
  so the guest driver puts partial-checksum and GSO metadata in each frame's
  `virtio_net_hdr`.
- On transmit it `writev`s the guest's descriptor chain to the TAP as-is,
  `virtio_net_hdr` included, straight from guest memory. It never inspects the
  header, and the guest can change it while the host is writing.
- Customer images without a `USER` run as uid 0 in the guest
  (`pkg/oci/image.go` `ImageProcessUser`). Root can send raw frames with any
  `virtio_net_hdr` (`PACKET_VNET_HDR`), so no guest kernel exploit is needed.
  Disabling offloads in our guest kernel would not help either.

This is a class of bug, not one bug. `virtio_net_hdr` handling has produced
several host CVEs (GSO type and size confusion, `hdr_len` underflow), and each
is reachable the same way. The jailer and seccomp limit what a compromised VMM
can do. They do nothing against a host kernel memory bug.

## Decision

Production compute nodes run a Firecracker built from the pinned upstream commit
plus a Gregale patch (`deploy/firecracker/patches/v1.17.0/`). On the TAP path,
the host never passes guest offload metadata to the kernel.

1. **No offloads are offered.** New net devices advertise only `VERSION_1`,
   `MRG_RXBUF`, `EVENT_IDX` (and `MAC`/`MTU` when configured). There is no
   checksum or segmentation offload in either direction, and the TAP gets
   `TUNSETOFFLOAD 0`. The guest computes its own checksums and segments to the
   MTU. The host delivers fully checksummed, segmented frames.
2. **The host owns the header.** Every frame goes to the TAP as
   `[host vnet header][guest frame after its header]`.
   - If the device never acked a guest-to-host offload, the host header is all
     zero, whatever the guest wrote. A guest header that asked for an offload
     is counted in `tx_offload_metadata_ignored`.
   - The payload is still read from guest memory, so there is no extra copy.
     Without offload metadata the kernel parses the payload only from its own
     copy.
3. **Devices restored from older snapshots** keep their saved feature set, so a
   guest that acked `CSUM`/`HOST_TSO*` keeps sending offload frames. Breaking
   those would break networking for every VM resumed from an existing snapshot.
   For these devices `offload_guard::validate_legacy_offload` accepts exactly
   one shape:
   - The frame is an untagged Ethernet frame carrying IPv4 (not a fragment) or
     IPv6 with no extension headers, then TCP or UDP. It is parsed from a host
     copy of the first 134 bytes.
   - `csum_start` equals the transport header offset the host found, and
     `csum_offset` is 16 (TCP) or 6 (UDP).
   - GSO is only TCPv4/TCPv6 (with the ECN bit allowed), matching the parsed
     protocols, with a non-zero `gso_size`. Each offload must have been acked
     by this device.
   - The IP length covers the headers and fits the frame.

   The host then writes its own header (host-computed `csum_start`,
   `csum_offset` and `hdr_len`) and the validated headers from its copy, so the
   guest cannot change them after validation. Anything else is dropped and
   counted in `tx_offload_rejected`, and accepted frames in
   `tx_offload_legacy_accepted`. UFO is refused outright, since Linux guests
   stopped generating it in 4.14.
4. **Build and ship.**
   - `scripts/build-firecracker.sh` checks out `fc_source_commit` and requires
     the `fc_release` tag to name it. It applies the patch series in order and
     builds a static musl binary with the toolchain the Firecracker revision
     pins (its seccomp filters assume it). It runs the pure validation tests
     before building.
   - Release CI ships the binary in the signed bundle as `firecracker-vmm` and
     writes its sha256 to `release.firecracker_digest`. The distinct name keeps
     it from shadowing `firecracker` on vmmd's PATH.
   - `node_join` now verifies the bundle signature before bootstrap extracts
     anything from it, then stages `firecracker-vmm`.
   - The `firecracker` role installs it as
     `/usr/local/bin/firecracker-v1.17.0-x86_64`, the upstream basename, so the
     jailer's chroot naming and vmmd's native recovery are unchanged. The
     existing release-anchor assertion then requires the manifest digest.
   - A release without `firecracker-vmm` (older, or a rollback) re-extracts
     upstream's binary, which its manifest names.
   - The jailer stays upstream's.

The patched binary still reports `Firecracker v1.17.0`, and the snapshot state
format is unchanged, so snapshots move freely between patched and unpatched
hosts. A snapshot taken on a patched host of a freshly booted VM carries no
offload features. Restoring it on an unpatched host is also safe for that VM,
because its driver never negotiated an offload.

## Consequences

- PwnEscape and the rest of the `virtio_net_hdr` class are unreachable from
  freshly booted guests, whatever host kernel runs. This holds until the host
  kernel is patched and after. Host kernel patching (drain and reboot once
  Ubuntu ships `linux-gcp-7.0`) is still required, for other paths into the
  same code.
- VMs resumed from snapshots taken before this change keep offloads until their
  next cold boot, but only in the validated shape above. That shape is what a
  correct Linux driver sends, and it cannot place a checksum outside the
  transport header. Old snapshots age out as they are rebuilt. A later change
  may mark them stale to force the switch.
- **Throughput.** Guest-to-host bulk traffic loses TSO: the guest kernel
  segments to the MTU and checksums in software, and the host sees more packets
  per byte.
  - Measured 2026-11-08 on `gregale-internal-test-1` (n2, nested KVM) with
    `TestMetalGuestTransferThroughput`. The guest was uncapped, 2 vCPU, and
    each test ran 5× 64 MiB GETs plus 300 small GETs, two rounds per binary,
    with the rc.330 guest kernel:

    | Firecracker v1.17.0 | bulk guest→host (median MiB/s) | small GET p50 / p99 (ms) |
    | --- | --- | --- |
    | upstream | 530, 1103 | 2.9 / 9.5, 2.4 / 7.2 |
    | patched | 115, 116 | 3.2 / 7.9, 1.1 / 2.4 |

  - Bulk throughput falls about 5–10×, to roughly 970 Mbit/s per VM.
    Small-request latency is unchanged within noise.
  - Customer instances never get near that ceiling. The plan cap (10, 25, 100
    or 250 Mbit/s, a `tbf` on the netns's `VethPeer`) shapes everything a
    guest sends, HTTP responses included. The patch therefore costs host CPU
    per byte, not customer-visible bandwidth.
  - Uncapped VMs (`EgressMbit` 0, such as builders) do see the lower ceiling.
  - Host-to-guest throughput, which loses GRO/GUEST_TSO, was not measured.
- Each release now builds Firecracker (a few minutes in the release job).
  `release.firecracker_digest` changes when the patch, the pin or the toolchain
  changes, and new runtime qualifications record it.
- The new counters live in Firecracker's metrics, which vmmd does not collect
  today. Collecting them, and alerting on any `tx_offload_rejected`, is a
  follow-up. A non-zero value means a guest sent offload metadata no correct
  driver produces.
- Upgrading Firecracker now means rebasing the patch series. A patch that no
  longer applies stops the release build.

## Alternatives considered

- **Invalidate every snapshot and strip offloads everywhere.** This is the
  simplest patch, but every parked app would cold-boot once.
- **Complete checksums and segment in userspace for legacy devices.** This is
  fully transparent, but means much more VMM code (TCP segmentation) for a
  transitional case.
- **Keep host-to-guest offloads.** They are not a guest-controlled input. They
  were dropped too, at the operator's choice, to keep the device surface
  minimal.
- **Host-side tc/eBPF filter on each TAP.**
  - tc programs run after `tun_get_user` has already turned the guest header
    into skb state.
  - `__sk_buff` exposes neither `csum_start` nor `ip_summed`, so the filter can
    only guess from indirect signals.
  - It would also be per-node state outside the VMM.
  - It remains an option for hosts that cannot take a new VMM.
