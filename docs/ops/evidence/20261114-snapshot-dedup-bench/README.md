# Snapshot dedup bench, 2026-11-14

`TestMetalSnapshotDedupBench` (pkg/fcvm/snapshot_dedup_bench_metal_test.go) on
gregale-internal-test-1: Firecracker 1.17.0, guest kernel 6.18.54, free page
reporting on, fixtures on an XFS reflink=1 loop file over the scratch
persistent disk. Seven apps (5 Node 22, 2 Python 3.13; 512 MiB and 2 GiB),
10 restores per measurement. Full output: run3-results.txt (the passing run).

Storage (memory): today 405.5 MB for 7 snapshots; fleet chunk store 4 KiB
127.5 MB (69% less, ~17 MB unique per app), 16 KiB 235 MB (42%), 64 KiB
312 MB (23%). Drive as a delta against its layer: 7 blocks (28 KB) per park.

Cold restore p50 (wake / first request), node-b 512 MiB and node-c 2 GiB:

| layout | node-b | node-c |
|---|---|---|
| captured | 1.08 s / 1.07 s | 1.04 s / 1.67 s |
| image pass (ADR-956) | 1.42 s / 1.70 s | 6.24 s / 11.2 s |
| image + peer pass (prod today) | 7.59 s / 9.76 s | 6.98 s / 14.1 s |
| 4 KiB chunks, reflink-assembled | 6.20 s / 5.74 s | 7.54 s / 9.92 s |
| 16 KiB chunks | 6.96 s / 8.28 s | 7.35 s / 12.8 s |
| 64 KiB chunks | 1.23 s / 1.03 s | 4.01 s / 5.51 s |
| drive rebuilt from layer + delta | 1.10 s / 1.01 s | 1.30 s / 1.84 s |

Warm restores are 210–470 ms in every layout. Cache miss with 4 KiB chunks:
fetch 13.5–17.9 MB instead of 59.7–65.7 MB; local decode/assemble 1.8–2.7 s
instead of 3.7–3.9 s.

Caveat: cold numbers are on a loop file over a persistent disk with all page
cache dropped; prod /srv/fc is XFS directly on pd-ssd. Re-measure the
fragmentation penalty there before acting on it.
