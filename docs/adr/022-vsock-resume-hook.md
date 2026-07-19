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
write [4B msg type=1]               |                                     |
       [4B body len=N]              |                                     |
       [N bytes JSON body]          |                                     |
                                    |  → forwarded to guest's accepted conn
                                    |                                     |  read 4B msg type + 4B body len
                                    |                                     |  read N bytes JSON body
                                    |                                     |  RunResumeHook(nano)
                                    |                                     |  write ack[1]
write ack[0] ← read                 |                                     |
```

The wire carries a **4-byte big-endian body length** between msg-type and
JSON body so the guest reads exactly N bytes instead of waiting for EOF.
The earlier "host calls CloseWrite" pattern broke under Firecracker's
vsock proxy, which doesn't always relay the half-close promptly — the
guest's `ReadAll` would hang until the read deadline, then EOF mid-ack,
and the resume hook's failure on a healthy guest would force a cold-boot
fallback (ADR-005). Length-prefixed framing is the only wire format
guaranteed to round-trip through FC's vsock proxy in the V6 metal test.

## CID allocation

```
host CID                       = VMADDR_CID_HOST (2, Linux kernel) — not used by us
guest CID for slot N           = 0x100 + N
reserved (skipped)             = 0, 1, 2 (well-known), < 3 (FC min)
guest listener bind CID        = VMADDR_CID_ANY (0xffffffff) — wildcard on own CID
```

The guest-init listener binds on `VMADDR_CID_ANY`, NOT on the host's
`VMADDR_CID_HOST = 2`. The Linux kernel reports the host's CID as 2
(its well-known hypervisor CID), not 3 as Firecracker's docs sometimes
imply; binding on 2 targets the host kernel's vsock namespace, not the
guest. `VMADDR_CID_ANY` accepts inbound on whatever CID Firecracker
assigned this instance (the slot-derived `guest_cid` above) — that is
the CID the host dials via FC's CONNECT-port handshake.

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

## Post-pivot /dev, /proc, /sys remount (guest-init)

The earlier `mountBasics()` mounts `proc`, `sysfs`, `tmpfs`, and
`devtmpfs` at the corresponding top-level dirs on the OLD root filesystem.
After `pivot_root()` into the merged overlay, those mounts are gone —
the new root's `/dev` is whatever the base layer shipped (just
`null/console/tty` from a hand-rolled `mknod` build step). Without a
fresh `devtmpfs` mount on the new root, the guest has no `/dev/hwrng`,
`/dev/urandom`, `/dev/zero`, etc.; without `/proc` the resume hook
cannot read `/proc/sys/kernel/random/uuid` to record its freshly-rekeyed
value (spec §11 V6).

`pivotInto()` re-mounts devtmpfs, proc, sysfs, and tmpfs on the new root
after pivot. Each mount is best-effort (a Warn, not a fatal — the
platform must stay up even if one remount fails); the resume hook's
nack-and-cold-boot-fallback (ADR-005) is the safety net for a half-fixed
guest.

The base layer still ships a small set of `mknod`'d device files
(`/dev/null`, `/dev/console`, `/dev/tty`) so the kernel can open them at
very early boot — before `pivotInto` runs. After pivot, devtmpfs takes
over.