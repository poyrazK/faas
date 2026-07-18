# ADR-022 · Post-restore resume hook over AF_VSOCK

- **Status:** accepted
- **Date:** 2026-07-18
- **Decision:** Use an AF_VSOCK device on every Firecracker microVM (cold-boot
  AND restore) to deliver the post-restore resume hook. The host (vmmd)
  dials the guest from CID 3 to the slot-derived guest CID on port 1024;
  the guest runs `RunResumeHook(hostTimeUnixNano)` (re-seed entropy + step
  clock) and writes a 1-byte ack. The hook fires before `waitReady`, so the
  app cannot accept `:8080` until entropy is fresh.
- **Why:** Spec §11 V6: two restores from one snapshot must yield distinct
  `/proc/sys/kernel/random/uuid` immediately post-resume. Without this hook
  every restored VM inherits its snapshot's RNG stream — UUID collisions
  for anyone generating correlation IDs, TLS nonces, or session tokens. The
  V6 acceptance test would fail on every concurrent restore.
- **Consequences:**
  - `pkg/fcvm.VMM` interface gains `TriggerResumeHook(ctx, l, hostTimeUnixNano)`.
  - `JailerVMM.Boot` PUTs `/vsock` after `startJailer` (cold-boot attaches
    the device; no hook call).
  - `JailerVMM.Restore` PUTs `/vsock` then calls `TriggerResumeHook` between
    `/snapshot/load` PUT and `waitReady`. Failure propagates; the
    `Manager.bringUp` ADR-005 fallback cold-boots.
  - `guest/init/main_linux.go::boot` binds the AF_VSOCK listener on
    CID 3 / port 1024 before `sup.Run()`. Listener is multi-shot; production
    dials once.
  - Wire format (host → guest): `[4-byte BE msg type][JSON body][CloseWrite()]`
    then host reads `[1-byte ack]`. 4-byte discriminator = 1 today
    (`MSG_RESUME`); new msg types extend the space without a wire break.
  - Guest kernel MUST have `CONFIG_VSOCKETS=y`. Built into the M5
    pinned `vmlinux-6.1`; the README documents this as a build-time
    invariant.
- **Rejected alternatives:**
  - **Timer-based re-seed (sleep N seconds inside the listener)**
    Race-prone and slow. Vsock tells us "guest kernel is up and PID 1 is
    listening"; a timer tells us nothing about the guest's actual state.
  - **Continue without hook on restore**
    Fails V6 outright. Every restored instance keeps the snapshot's RNG
    state, so UUID collisions are guaranteed.
  - **Fold `TriggerResumeHook` into `Restore`**
    Kills unit-test isolation. We need a fake UDS listener and a fake FC
    API server; combining them forces an awkward double-fake or end-to-end
    mock that hides dial-vs-ack bugs behind the snapshot path. The split
    interface lets the dial path be exercised without a Firecracker.

## Wire format (canonical reference)

```
[ 4 bytes big-endian uint32: msg type, currently 1 = MSG_RESUME ]
[ JSON body: {"hostTimeUnixNano": <int64>} (currently empty {} for unknown types) ]
[ 1 byte ack: 0 = OK, 1 = NACK ]
```

After writing the request, the host calls `CloseWrite()` so the guest's
`io.ReadAll` on the body unblocks. Without this, the server-side reader
hangs on EOF until the read deadline (unix socket semantics).

## CID allocation

```
host CID                       = 3     // Firecracker convention
guest CID for slot N           = 0x100 + N
reserved (skipped)             = 0, 1, 2 (well-known), < 4096 (hypervisor)
```

`Lease.Slot` is the unique-while-live root for UID/GID/HostIP/VethHost
(alloc.go:113-124); CID derivation reuses the same invariant instead of
adding a 4th allocation dimension. ADR documents the choice so a future
maintainer doesn't add a parallel allocator.

## Ordering on Restore

```
1. startJailer → jail chroot at <base>/<fcName>/<inst>/root/
2. PUT /snapshot/load (spec.VsockDevice still nil; RestoreSpec carries it)
3. PUT /vsock   ← restores the device config so jailer creates vsock.sock
4. TriggerResumeHook on <inst>/root/vsock.sock
   - retry dial with 200 ms per-attempt timeout, 20 ms backoff
   - 5 s outer deadline
   - 2 s per-write/read deadline
5. waitReady → app may now bind :8080
```

Step 4 is non-skippable on restore. On cold-boot, steps 3-4 reduce to "PUT
/vsock, never dial" — fresh kernel entropy doesn't need the hook.

## Cold-boot fallback (ADR-005)

`Manager.bringUp` cold-boots on any restore error. Cold-boot does NOT call
`TriggerResumeHook`; entropy is fresh by construction. This means a
broken-vsock guest degrades to "no snapshots" but the platform stays up —
consistent with ADR-005's "snapshots are cache, not truth" stance.