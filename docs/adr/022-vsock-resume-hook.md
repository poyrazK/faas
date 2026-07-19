# ADR-022 · Post-restore resume hook over AF_VSOCK

- **Status:** accepted
- **Date:** 2026-07-18
- **Decision:** Use an AF_VSOCK device on every Firecracker microVM
  (cold-boot AND restore) to deliver the post-restore resume hook. The
  vsock device is configured via the config-file (top-level `vsock:`
  field, FC pre-boot only). The host (vmmd) dials the resulting
  `<chroot>/vsock.sock`, performs the FC host-initiated CONNECT-port
  handshake to bridge into the guest's AF_VSOCK listener on port 1024,
  and writes the resume-hook payload. The guest runs
  `RunResumeHook(hostTimeUnixNano)` (re-seed entropy + step clock) and
  writes a 1-byte ack. The hook fires before `waitReady` on Restore,
  so the app cannot accept `:8080` until entropy is fresh.
- **Why:** Spec §11 V6: two restores from one snapshot must yield distinct
  `/proc/sys/kernel/random/uuid` immediately post-resume. Without this hook
  every restored VM inherits its snapshot's RNG stream — UUID collisions
  for anyone generating correlation IDs, TLS nonces, or session tokens. The
  V6 acceptance test would fail on every concurrent restore.
- **Consequences:**
  - `pkg/fcvm.VMM` interface gains `TriggerResumeHook(ctx, l, hostTimeUnixNano)`.
  - `JailerVMM.Boot` writes the vsock config into the config-file JSON
    (`BuildColdBootConfig` sets `VsockDevice`). FC attaches it pre-start;
    the UDS at `<chroot>/vsock.sock` is live by the time `startJailer`
    returns. **No post-start PUT /vsock** — FC rejects that with 400
    "operation is not supported after starting the microVM".
  - `JailerVMM.Restore` calls `TriggerResumeHook` between
    `/snapshot/load` PUT and `waitReady`. Failure propagates; the
    `Manager.bringUp` ADR-005 fallback cold-boots.
  - `guest/init/main_linux.go::boot` binds the AF_VSOCK listener on
    CID 3 / port 1024 before `sup.Run()`. Listener is multi-shot; production
    dials once.
  - Wire format follows FC's host-initiated vsock protocol (see FC
    docs/vsock.md): host writes ASCII `CONNECT <port>\n`, FC replies
    `OK <hostside_port>\n`, then byte stream is bridged to the guest's
    listener. Payload after the handshake: `[4-byte BE msg type][JSON body]`,
    host reads `[1-byte ack]`. The 4-byte discriminator = 1 today
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
    Kills unit-test isolation. We need a fake UDS listener; combining
    with the snapshot path forces an awkward double-fake that hides
    dial-vs-ack bugs. The split interface lets the dial path be
    exercised without a Firecracker.
  - **PUT /vsock post-start**
    Rejected by FC's pre-boot-only device-attachment model. The
    config-file path is the only legal way to attach a vsock device.

## Wire format (canonical reference)

```
HOST                                |  FIRECRACKER UDS                    |  GUEST AF_VSOCK :1024
----------------------------------- | ----------------------------------- | ----------------------------
connect <uds_path>                  |                                     |
write "CONNECT 1024\n"              |                                     |
                                    |  accept (host)                      |
                                    |  → guest AF_VSOCK listen on :1024   |
                                    |  reply "OK <hostside_port>\n"       |
read "OK ...\n"                     |                                     |
write [4B msg type=1][JSON body]    |                                     |
                                    |  → forwarded to guest's accepted conn
                                    |                                     |  read 4B msg type
                                    |                                     |  read JSON body until EOF
                                    |                                     |  RunResumeHook(nano)
                                    |                                     |  write ack[1]
write ack[0] ← read                 |                                     |
```

After writing the payload, the host calls `CloseWrite()` so the guest's
`io.ReadAll` unblocks (unix socket semantics — without the half-close the
guest's reader hangs on EOF until the read deadline).

## CID allocation

```
host CID                       = 3     // Firecracker convention
guest CID for slot N           = 0x100 + N
reserved (skipped)             = 0, 1, 2 (well-known), < 3 (FC min)
```

`Lease.Slot` is the unique-while-live root for UID/GID/HostIP/VethHost
(alloc.go:113-124); CID derivation reuses the same invariant instead of
adding a 4th allocation dimension. FC swagger requires `guest_cid ≥ 3`;
`VsockCIDBase = 0x100` satisfies with room for 3840 instances.

## Ordering on Restore

```
1. write config-file → <chroot>/vmconfig.json with `vsock: {...}`
2. startJailer → jail chroot at <base>/<fcName>/<inst>/root/
   (firecracker reads config-file, attaches vsock pre-start, creates
    <chroot>/vsock.sock)
3. PUT /snapshot/load  (snapshot data is bridged into the running VM)
4. TriggerResumeHook on <chroot>/vsock.sock
   - FC CONNECT-port handshake: "CONNECT 1024\n" → "OK <port>\n"
   - resume payload: [4 BE msg type][JSON body]
   - 5 s outer deadline, 200 ms per-attempt dial timeout, 20 ms backoff
5. waitReady → app may now bind :8080
```

Step 4 is non-skippable on restore. On cold-boot, step 4 is omitted —
fresh kernel entropy doesn't need a hook.

## Cold-boot fallback (ADR-005)

`Manager.bringUp` cold-boots on any restore error. Cold-boot does NOT call
`TriggerResumeHook`; entropy is fresh by construction. This means a
broken-vsock guest degrades to "no snapshots" but the platform stays up —
consistent with ADR-005's "snapshots are cache, not truth" stance.