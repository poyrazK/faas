# ADR-642 · Guests boot with restore-safe timers

- **Status:** proposed
- **Date:** 2026-10-07
- **Amends:** spec §4.4 (guest kernel command line), ADR-510 (snapshot backing
  identity, document version 2), ADR-005 (a refused restore cold-boots).
- **Decision:**
  - **Boot args.** Every guest (app, execution and job) boots with
    `clocksource=kvm-clock lapic=notscdeadline`, so the kernel keeps
    paravirtual time and programs the one-shot LAPIC timer instead of TSC
    deadlines. A restore keeps the clocksource and clockevent of the
    original cold boot.
  - **Backing identity v2.** The ADR-510 identity records the timer profile.
    Version-1 captures (all of them booted with the default `tsc` /
    `lapic-deadline`) and captures with a different profile are refused. The
    wake cold-boots, schedd marks the snapshot stale and the next park
    captures the new profile.
- **Why:** H5-25, from production-us hunt #5 (rc.245, Firecracker 1.7.0).
  - **Symptom:** after some restores, guest timers stopped firing on time.
    - A Node app's 1 s `setInterval` ran every 5 s; a `setTimeout(1 s)` took
      5 s, and one took over 30 s.
    - This lasted for minutes. Synchronous handlers answered in 260 ms, the
      guest wall clock was correct, the host was idle and the VM was not
      CFS-throttled.
    - A 60-request wake storm all ended in `504`. Liveness killed the VM 82 s
      after a restore that had itself been healthy (510 ms, readiness in
      217 ms).
  - **Scope:** 3 of 6 restores of park captures of cold-booted VMs that had
    run for 1.5–5 minutes stalled. 12 of 12 restores of the deploy's prime
    capture (taken at 8.8 s uptime) were clean.
  - **Guest:** 4 vCPUs, `clocksource=tsc`, `clockevent=lapic-deadline`.
  - **Cause:** this matches firecracker#4099. After a restore, KVM evaluated
    `MSR_IA32_TSC_DEADLINE` against a TSC that Firecracker 1.7 wrote
    afterwards, so armed deadlines fired late. Firecracker 1.8 restores the
    deadline after the TSC (#4666) and replaces a zero deadline (#4618).
    firecracker#6200, unreleased, additionally fixes vCPUs coming back with
    different TSC offsets, which affects the `tsc` clocksource.
  - **Why not just upgrade:** that is the right long-term fix, but it changes
    the VMM's API and CPU templates, and invalidates every snapshot
    (`snapshots.fc_version`). It is tracked separately. The boot args fix 1.7
    now and stay correct after an upgrade.
- **Consequences:**
  - After the rollout, each app's first wake on each node cold-boots once,
    then restores again from the recaptured snapshot (ADR-005).
  - Timer programming exits to the hypervisor slightly more often (one-shot
    LAPIC instead of TSC deadline). The guest reads `kvm-clock` through the
    vDSO, so `clock_gettime` stays in userspace.
  - Warm-tier and migration captures follow the same rule as park captures.
- **Verification:**
  - `TestDiagnosticGuestTimersAfterRestore` (metal) cold-boots a 4-vCPU
    guest running a 250 ms tick loop, lets it run, parks it and restores the
    capture five times. It counts ticks after each restore.
  - `FAAS_DIAG_GUEST_TIMER=legacy` runs the old profile as the control.
  - Internal nested KVM host (Firecracker 1.7.0, guest 6.1.134), 90 s of
    runtime then 5 restores per profile, 2026-10-07:
    - **New profile:** the guest reports `kvm-clock` / `lapic`; 0 of 5
      restores stalled (31 ticks in 8 s, longest gap 207 ms). Cold boot took
      1.258 s against 1.167 s for the legacy profile.
    - **Legacy control:** 0 of 5 stalled as well. The busy tick loop keeps a
      timer armed, while production's stalls followed minutes of idle.
    - The run therefore shows the profile boots and restores correctly. That
      it cures the stall rests on removing the TSC-deadline clockevent, which
      is the restore path firecracker#4099 broke. Re-check the stall rate on
      production after the rollout.
  - Native x86_64 acceptance (`make test-metal`, `leakcheck`) is still
    required by spec §14.
- **Rejected alternatives:**
  - Only change `clocksource`. The lost interrupts come from the TSC-deadline
    clockevent; kvm-clock alone does not avoid them.
  - Kick timers from guest-init after a restore (for example by re-setting
    the clock). The resume hook already steps the wall clock with
    `settimeofday`, which re-triggers hrtimers, and the stalls happened
    anyway.
