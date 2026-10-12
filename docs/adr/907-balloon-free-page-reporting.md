# ADR-907: Balloon free page reporting

- Status: Accepted
- Date: 2026-10-12
- Related: ADR-005, ADR-159, ADR-225, ADR-983, ADR-906
- Amends: spec §4.4 boot config ("balloon off (v1)")

## Context

A guest frees memory all the time: garbage-collected heaps, released
buffers, exited processes. Until now the host never learned about it. Every
page the guest had touched stayed resident in the Firecracker process until
the VM was destroyed. Every snapshot also carried the stale contents of
freed pages, which costs disk, upload bytes and restore page faults.

Firecracker 1.14 added free page reporting to its balloon device. The guest
reports free 2 MiB blocks to the host, which discards them. The guest
kernel built from the v1.17 6.18 config (ADR-983) already includes
`VIRTIO_BALLOON` and `PAGE_REPORTING`. Spec §4.4 left the balloon off in v1,
because inflating a balloon would take plan RAM from the guest.

## Decision

1. vmmd attaches a balloon to every cold boot (app, job and builder VMs)
   when the detected Firecracker is 1.14 or newer. The balloon is
   `amount_mib = 0` and never inflates, so the guest keeps all of its plan
   RAM. It sets `deflate_on_oom = true` and `free_page_reporting = true`,
   with statistics off.
2. Restored VMs keep the devices their snapshot recorded. Snapshots taken
   before this change restore without a balloon, and the next park writes
   one that has it.
3. `disable_free_page_reporting = true` in vmmd.toml, or
   `FAAS_FREE_PAGE_REPORTING=0`, turns it off. An older or undetected
   Firecracker never gets the field, because it would reject the config.
4. Billing and admission are unchanged: plan RAM + 8 MB per running second,
   and invariant 2 still sums reservations. The change gives back real host
   memory (page cache and headroom), shrinks snapshots, and is the
   prerequisite for measured admission and frozen instances (ADR-906).
5. Free page hinting is not used. It needs a host-driven run before each
   snapshot (about 50 ms), and after reporting it removed only 13 MB more
   (see Evidence).

A discarded page reads back as zero for anonymous memory. For a VM restored
from a snapshot (a private file mapping), it reads back as that page's
content in the snapshot. That is data the same guest already had, never
another tenant's, and the guest kernel does not assume freed pages are zero.
The balloon runs inside the jailed, seccomp-filtered Firecracker process
like every other device (§11).

## Evidence

Raw Firecracker 1.17, guest 6.18, n2 test node. Node 22 app with about
250 MB resident state, 1 GiB guest; the app allocates 320 MiB, then frees
it and runs GC.

Cold-booted VM:

| | host RAM after the free (+6 s) | non-zero content of the next Full snapshot |
|---|---|---|
| no balloon | 578 MB | 566 MB |
| free page reporting | 313 MB | 301 MB |
| free page hinting only | 358 MB (after a hinting run) | 346 MB |
| both | 302 MB | 288 MB |

VM restored from a snapshot (production's shape), six interleaved restores
each:

| | restore → first byte p50 | after alloc | after free (+6 s) |
|---|---|---|---|
| no balloon | 64 ms | 351 MB | 355 MB |
| free page reporting | 64 ms | 354 MB | 95 MB |

App data read back identically before and after the reported pages were
discarded.

vmmd metal qualification: see "Qualification".

## Measured and rejected on Firecracker 1.17

Same node, `TestMetalRestoreTimingBench` (1 GiB guest, 20 restores each,
wall p50/p90 ms), and the raw harness above:

- **Huge pages on snapshot load** (`huge_pages: "Transparent"`): slower,
  218/318 against 192/208 with a warm page cache, and 435/891 against
  316/479 with a cold one, where the resume hook rose from 54 to 136 ms.
- **`clock_realtime` on snapshot load**: 184/218, within noise of the
  baseline. The resume hook still steps the clock and stays fault-bound.
- **mincore-based diff snapshots** (no dirty tracking): on a file-backed
  restore every mapped page counts as resident, so the "diff" was the full
  1024 MB.
- **Diff snapshots with dirty tracking** work: after a restore and some
  traffic, the diff took 0.70 s and 94 MB against 8.9 s and 1024 MB for a
  Full snapshot on the same disk, and a merged image (base plus diff)
  restored and served. They are not pursued, because idle park already
  reuses the existing capture instead of writing a new one (ADR-159). A
  capture is written only for a new deployment, a stale snapshot or a warm
  tier, and those start from a cold boot, where no base exists to diff
  against.
- **UFFD memory backend** (a page-fault handler process instead of the File
  backend), tested with an experimental Go handler, Node app, 1 GiB guest,
  3 rounds. Warm restore to first byte: 62–67 ms with File, against
  144–149 ms on demand (26,000 faults through userspace) and 120–136 ms when
  the handler eagerly copied every non-zero page. A CPU-bound request after
  restore took 405–412 ms against 1.73–1.81 s on demand. Every page UFFD
  serves becomes a private copy in the VM: 101–252 MB per VM, against
  14 MB with File, which shares the snapshot's page cache. UFFD won only
  with a dropped page cache (1.2–1.3 s against 1.8–2.4 s), which ADR-225's
  prefetch already addresses. It is worth revisiting only to load snapshots
  lazily from a remote store.
  At scale it does worse. Six restores of one snapshot started at once on
  the 2-vCPU node (two rounds) reached first byte at p50 608–737 ms with
  File against 1.34–1.54 s with UFFD on demand. The following CPU-bound
  request took ~2.0 s against 13–15.6 s, because faults were serialized
  through the userspace handlers. The host paid 4.5 MB per VM against
  109 MB.
- **Guest `mitigations=off`**: boot, request latency, CPU-bound requests and
  restore were unchanged within noise over 3 interleaved rounds, so the
  guest keeps its default mitigations.
- **UFFD handler variants** (2026-10-12, Node app, 1 GiB guest), first byte
  and the next CPU-bound request, warm:

  | handler | first byte | CPU-bound request | private memory per VM | faults |
  |---|---|---|---|---|
  | File (today) | 65–93 ms | 414 ms | 14 MB | — |
  | on demand, 4 KiB | 170–177 ms | 1.73–1.82 s | 104 MB | 26,000 |
  | 64 KiB fault-around, zero pages mapped | 107–110 ms | 580 ms | 172 MB | 2,730 |
  | 2 MiB fault-around | 125–307 ms | 406–417 ms | 246 MB | 126 |
  | eager copy of non-zero pages | 157–168 ms | 431–502 ms | 237–252 MB | 544–1,021 |

  With a cold page cache, 2 MiB fault-around ran the CPU-bound request in
  695–730 ms against File's 1.33 s. Six concurrent restores reached first
  byte at p50 779 ms against File's 655 ms, and held 164 MB of host memory
  per VM against 6.7 MB. No variant beats File for local snapshots; the
  shared page cache is worth more than fewer faults.
- **2 MiB huge pages for guest memory** (`huge_pages: "2M"`): the File
  backend refuses to restore a hugetlbfs-backed snapshot. Through UFFD it
  restores to first byte in 78–82 ms, and the next CPU-bound request took
  28 ms against 363 ms for a 4 KiB guest restored from File. But every VM
  pins its whole guest RAM in hugetlb pages, so free page reporting,
  reclaim and overcommit all stop working. That fits a dedicated
  latency-first tier, not the shared fleet.

## Qualification

These ran on the n2 node with vmmd's metal suite, the balloon attached to
every cold boot (`FAAS_TEST_FREE_PAGE_REPORTING=1`), Firecracker 1.17 and
guest 6.18.

- `TestMetalFreePageReportingBalloon` passed: a cold boot reports
  `free_page_reporting` with `amount_mib = 0`, and so does the VM restored
  from its park snapshot.
- `TestMetalParkWakeCycle` passed: 100 park/restore cycles, p50 267 ms,
  p95 286 ms.
- `TestMetalHelloBoot`, `TestMetalDNATPublishedToGuestPort` and
  `TestMetalMemoryMaxFenceEnforced` passed their own checks. They then failed
  only their final zero-leak assertion, on 14 jail chroots, a TUN mount and
  a builder cgroup that other sessions left on the shared node on
  2026-10-08/09. The same leftovers failed these tests on both Firecracker
  versions in ADR-983's qualification.
- `TestMetalRestoreTimingBench`, 1 GiB guest, 2 rounds × 10 restores,
  interleaved: wall p50/p90 196/215 ms without the balloon and 200/225 ms
  with it, within noise.
- The whole `pkg/fcvm` metal package with the balloon: 1,061 passed and 34
  failed. No test failed that passes without the balloon in ADR-983's
  baseline run (1,057 passed, 35 failed, all caused by the node's leftovers
  and missing fixtures), and the run left no new leaks.
