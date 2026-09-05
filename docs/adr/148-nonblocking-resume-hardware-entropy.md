# ADR-148 · Do not wait for hardware entropy on snapshot resume

- **Status:** accepted
- **Date:** 2026-09-05
- **Amends:** spec §4.8 and ADR-022's hardware entropy mix.
- **Decision:** After injecting fresh host CSPRNG bytes and explicitly reseeding
  the guest kernel CRNG, mix at most 256 bytes currently available from
  `/dev/hwrng`. Open the device with `O_NONBLOCK` and read using the raw syscall.
  A missing device, an empty read, or `EAGAIN` skips this optional mix. Preserve
  other device and destination errors. Then update the clock, write the UUID
  marker, and acknowledge resume in the existing order.
- **Why:** The old `io.CopyN` waits until it obtains all 256 bytes. A restored
  hardware RNG with no data, or only a partial buffer, can hold the resume ACK
  indefinitely. Host entropy is already the authoritative input for restore
  uniqueness; waiting for optional hardware bytes adds a failure mode without
  strengthening that guarantee. An SSD canary had an ACK timeout, but that
  incident has not been attributed to this specific read.
- **Consequences:**
  - The host still supplies fresh CSPRNG entropy on every restore. Guest
    `RNDADDENTROPY` (or its existing write fallback) and `RNDRESEEDCRNG` remain
    before the clock, UUID marker, readiness, and ACK. Required reseed failures
    still reject restore and trigger the existing cold-boot fallback.
  - On Linux 6.1, writing to `/dev/urandom` mixes input; it does not credit
    initialization entropy. `RNDADDENTROPY` credits initialization entropy but
    does not by itself immediately rekey an already initialized CRNG. The
    explicit `RNDRESEEDCRNG` is required. This supersedes ADR-022's earlier
    explanation that a write consumes entropy and that credit alone suffices.
  - Virtio-rng remains attached. No changes to the snapshot wire protocol,
    the two-drive layout, guest isolation, or readiness requirements.
  - `O_NONBLOCK` avoids waiting for device data. It is not a universal syscall
    deadline: scheduling and the Linux hwrng driver's internal locks can still
    delay a read. This change does not establish the full-wake latency SLO.
- **Rejected alternatives:** Skip the mandatory host reseed; wait for a fixed
  amount of optional hardware data; put a blocking read in a detached goroutine
  that can survive indefinitely.
- **Validation:** Linux subprocess tests hold a FIFO writer open with zero,
  partial, full, or excess data. The reader must finish, mix only available
  bytes up to the cap, and retain real device/write errors. Hardware restore
  validation also requires unique UUIDs, no fallback, and no leaked leases or
  guest resources before deployment.

Linux 6.1 behavior: [hwrng read implementation](https://github.com/torvalds/linux/blob/v6.1/drivers/char/hw_random/core.c)
and [random device implementation](https://github.com/torvalds/linux/blob/v6.1/drivers/char/random.c).
