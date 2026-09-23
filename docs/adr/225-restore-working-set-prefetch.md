# ADR-225 · Restore working-set prefetch

- **Status:** accepted
- **Date:** 2026-09-23
- **Decision:** After a snapshot restore reaches readiness, vmmd reads the
  Firecracker process's page table (`/proc/<pid>/pagemap`) for the mapping of
  the snapshot's memory file and records the file ranges the guest touched,
  per snapshot family (`snap/<id>` — every capture of one deployment). On the
  next wake of that family, `Manager.Wake` issues `posix_fadvise(WILLNEED)`
  for the recorded ranges before it acquires the lease, prepares the network
  or waits for the restore gate, so the reads run while that work proceeds.
  The set is held in vmmd memory (bounded to 2,048 families, 1,024 ranges and
  256 MiB per family) and is not persisted. It is on by default;
  `disable_restore_prefetch = true` or `FAAS_RESTORE_PREFETCH=0` turns off both
  recording and prefetch.
- **Why:** A restore demand-faults guest memory from the mem file. On a
  production node the file is usually no longer in the page cache, because
  later parks write new snapshots and evict it; each fault then blocks a vCPU
  on a disk read. Production restore p50 was 181 ms (vmmd logs, 2026-09-23)
  against ~95 ms for a warm page cache; a mem-evicted restore of a real app
  measures ~190–220 ms, the production shape. Measured on fsn-4 (nested
  virtualization, like production compute) with real production app layers,
  1 GiB guests, mem evicted, prefetch interleaved off/on, 12 restores each:
  restore p50 186 → 95 ms (Go container) and 191 → 107 ms (Node function)
  when the wake builds its network inline; 221 → 170 ms and 211 → 143 ms when
  it takes a prepared network, which leaves less work to overlap.
  `resume_hook` returns to its warm-cache value (~32–48 ms from ~100–110 ms).
  Blind prefetch loses (see Rejected alternatives); only the recorded set,
  ~30–50 MiB for these apps, is read.
- **Consequences:** The first restore of a family after a vmmd restart, and a
  family's very first restore, get no prefetch. A capture whose layout differs
  from the recorded one prefetches some pages the guest will not touch; the
  byte cap bounds that waste. Prefetch applies only when the mem file is a
  local cached file; a remote snapshot is materialised by a full copy and is
  already resident. The `wake ok` log line gains `restore_prefetch_bytes` so
  the prefetch is visible in production. Nothing about what is restored
  changes: a failed record or fadvise affects latency only.
- **Rejected alternatives:** Blocking prefetch of the whole file or a prefix
  before `/snapshot/load` (measured in the ADR-192 investigation: full
  prefetch of a 256 MiB guest cost 2.2 s to save 0.6 s). Recording with
  `mincore` on the mem file (polluted whenever the cache is already warm or a
  prefetch ran, so the set grows toward the whole file). A userfaultfd memory
  backend with a recorded page order (the structural fix, and the only route
  to 2 MiB guest pages with Firecracker 1.7, but it replaces Firecracker's
  page-fault path and needs its own ADR). Persisting sets beside the snapshot
  in shared storage (couples a node-local cache hint to the storage contract).
