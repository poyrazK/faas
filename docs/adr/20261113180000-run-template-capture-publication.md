# ADR 20261113180000 — Run template capture and publication

Status: accepted for implementation; production enablement requires the operator gates below.

## Context

Run's format-1 catalog authenticates memory and machine state. The warm/OOM
qualification registered a trusted operator's native capture directly because
the pair-stream publisher cannot preserve the coupled writable drive and
backing identity. That fixture is not a production producer. Application
snapshot GC also has no knowledge of the platform-owned catalog namespace.

## Decision

1. Schedd owns an explicitly enabled background warmup worker. Operators supply
   approved release pins, a dedicated active platform Scale account and a bounded
   list of runtime/profile/memory/disk shapes. The worker never reads customer
   source, input, secrets or outbound grants. Cold Run dispatch does not wait for
   warmup. With the switch absent, no capture worker starts.
2. A new schedd-only `CaptureExecutionTemplate` RPC follows the existing agent
   and sandbox capture model. vmmd generates both UUIDs, boots a new networkless
   execution guest using platform base/layer keys, checks the actual kernel/base
   digests, waits for guest-init's pristine listener, requires its entropy/clock
   resume ACK, captures, and physically destroys the guest before returning.
   Template guests refuse ExecuteExecution. Capture is bounded to 90 seconds
   with one producer per vmmd. Execution-kind journals retain existing retirement
   authority after a crash; this change adds no adoption privilege.
3. Format 2 hashes the canonical identity plus the length and SHA-256 of memory,
   VM state, frozen private drive and backing identity. Reads have shape-based
   bounds; backing metadata must name the expected kernel/base and timer profile.
   Publication retains all four native objects in their original fresh namespace
   in vmmd's existing private native snapshot root, using exclusive writes. A
   scoped storage adapter disables shared/local-path shortcuts and refuses
   overwrites. Schedd has only a read-only native/canonical view; imaged asks
   vmmd to retire private objects. The durable insert-only catalog row is the
   publication commit point. The original functional cohort uses the existing
   execution retirement journal. The separately gated native-process producer
   and its reader requirements are defined in
   [the native-template recovery ADR](20261114010000-run-native-template-recovery.md).
4. Format-2 identities include Firecracker version. The additive `fc_version`
   column defaults empty for old format-1 writers. New readers still understand
   format 1; new Run resolution selects format 2. Older readers cannot select
   the new key and continue to cold-boot. The pair publisher remains format-1-only.
5. Ensure is idempotent for a verified ready row. A partial capture never inserts
   a row. Racing inserts preserve the sole durable winner and retire only the
   caller's distinct losing capture. After an uncertain database acknowledgement,
   a fresh lookup recovers a committed winner; unresolved uncertainty retains
   artifacts. Retirement is never overwritten. Permanently corrupt cache data
   falls back to cold execution; repair needs a reviewed release identity.
6. Imaged excludes the reserved platform namespace from deployment GC. Its
   catalog-aware collector retains ready and retired rows and removes only old,
   unreferenced captures, after the existing orphan grace. Read failures preserve
   data. Catalogued retired artifacts require a separate release-retention policy;
   retirement alone does not authorize physical deletion. Local collection
   refuses linked namespace directories. Remote-backend lifecycle remains its
   existing operator policy and is outside this acceptance cohort.
7. Schedd's disk-drift sweep includes catalogued runtime captures. The ordinary
   restore verifier authenticates all four objects before dispatch. The native
   clone test requires simultaneous live instances with separate jail UIDs,
   roots, writable drives and capped cgroups, plus guest-private scratch and RNG.
   Networkless Runs have no guest tenant NIC/netns/IP to allocate or compare.

## Rollout and qualification

Apply the additive migration, deploy catalog-aware imaged readers, then deploy
schedd/vmmd readers and the new RPC before enabling publication. Do not enable
while an older imaged can reclaim the platform namespace. Keep
`FAAS_EXECUTION_TEMPLATE_CAPTURE` unset until these steps and approved runtime
image checks pass. Enable it with the dedicated account UUID and explicit
`FAAS_EXECUTION_TEMPLATE_SHAPES` JSON; unset it to stop new capture after restart.
Existing ready rows remain readable and cold boot remains the fallback.

The producer currently supports local storage only. Configure vmmd's
`native_runtime_snapshot_root` as its existing root-owned private tree and set
the same absolute path in `FAAS_NATIVE_RUNTIME_SNAPSHOT_ROOT` for schedd and
imaged. Schedd reads that tree; imaged retires its objects through vmmd. Do not
enable capture with full native-process recovery without the additional flags,
compatible readers and qualification gates in the native-template recovery ADR.

The internal-node lane exercises this production code using isolated schemas,
private artifact stores and fixture image admission. It can establish bounded
functional evidence; it does not qualify production image provenance, remote
artifact storage, concurrent fleets, cross-node CPU compatibility, full billing,
default quarantine recovery or the repository's non-virtualized native release
gate. No production service rollout is authorized by running that lane.
