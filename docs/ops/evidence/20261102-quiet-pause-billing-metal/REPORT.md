# Unbilled quiet pause on native hardware

ADR-20261023171905. `TestQuietPauseBillingMetal` (cmd/e2e) runs the real
daemons on KVM: vmmd quiet-pauses a `between_requests` app 2 s after each
request, schedd writes the pause ledger, meterd bills each minute for its
running time and records the rest in `usage_minutes.paused_mb_seconds`.

## Run

- Host: gregale-internal-test-1 (native x86_64, KVM), shared acceptance lock.
- Revision: a73199c70e (main ff1fc06ddc plus the test fix in this change).
- Switches set by the test: `FAAS_QUIET_PAUSE=1`, `FAAS_QUIET_PAUSE_LEDGER=1`,
  `FAAS_QUIET_PAUSE_UNBILLED=on`, platform flag `quiet_pause_unbilled`
  released to every account (meterd apply receipt awaited).
- App: Pro plan, 1024 MB (billable 1032 MB), one request every 20 s.
- Result: `--- PASS: TestQuietPauseBillingMetal (645.92s)`, 2026-11-02T09:49Z.
- leakcheck before and after: clean (no leaked netns, taps, jails, cgroups,
  processes, bridges, mounts or native loops).

## Measured minutes

| Minute (UTC) | Billed | Recorded unbilled (quiet paused) | MB-seconds billed + unbilled |
|---|---|---|---|
| 09:45 | 7 s | 53 s | 7,224 + 54,696 |
| 09:46 | 7 s | 53 s | 7,224 + 54,696 |
| 09:47 | 7 s | 53 s | 7,224 + 54,696 |

Billing the whole minute, as without unbilled quiet pause, would be 60 s
(61,920 MB-seconds). Each minute bills its three requests at about 2 s each
plus the pause pass's tick, and billed plus recorded seconds make up the
minute's 60 s residency.

Ledger over the measured minutes: 8 closed pauses, 142.6 s paused. The test
asks for at least 8 of at least 15 s: the window counts only pauses that
start and end inside it, so the pause open across its start is not counted.

## What it does not cover

- The account usage summary read (`quiet_paused_gb_hours`) is covered by
  apid tests, not by this run.
- Production stays off: `quiet_pause_ledger`, meterd `quiet_pause_unbilled`
  and the platform flag all default off. The next step is the shadow week
  (rollout step 1).
