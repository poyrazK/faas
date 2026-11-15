# ADR-20261115100336: A restore keeps gunicorn's workers

- Status: Accepted
- Date: 2026-11-15
- Amends: ADR-680 (restore userspace RNG reseed: the Python preload),
  ADR-20261109120000 (restore advances the guest clock)

## Context

Since ADR-20261109120000 a restore advances the guest's monotonic clock by
the time the snapshot spent parked. gunicorn's master kills any worker whose
heartbeat file is older than `--timeout` (30 s by default), measured on
`time.monotonic()` (gunicorn 22 and later). On resume the master's 1 s
`select` returns at once, and on a one- or two-vCPU guest the master runs
before its workers have touched their heartbeat. It sees every worker silent
for the whole park and kills it.

Production-us, 2026-11-15, the `hello-python` template (gunicorn, two
workers): every wake logged `[CRITICAL] WORKER TIMEOUT`, a worker exiting
with code 1 and a replacement booting. When the replacement's imports
overlapped the resume hook, guest-init answered the host's vsock connect late:
`connect` took 1.1–1.6 s over 4–5 attempts, and `resume_hook` was ~1.4 s
against ~0.3 s for the same app when the replacement booted after the hook.
The same restores also lost a worker's in-memory state and served the first
requests with one worker booting.

## Decision

The Python restore preload (`guest/init/rngpreload/python/sitecustomize.py`)
wraps `gunicorn.arbiter.Arbiter.murder_workers` when gunicorn imports it.
The master calls it about once a second. When more than 5 s pass between two
calls, the master itself did not run, so the elapsed time says nothing about
its workers: the wrapper refreshes every worker's heartbeat and skips that
one check. A worker that really hangs is still killed `--timeout` seconds
later, by the next checks.

- The guard needs no restore signal, so it holds whichever process the guest
  schedules first, and also covers a host that stalls the guest for over 5 s.
- `GREGALE_GUNICORN_FREEZE_GUARD=off` in the app's environment disables it.
  `GREGALE_RESTORE_RESEED=off` also removes it, since the preload is then not
  injected.
- A gunicorn without `Arbiter.murder_workers` is left unchanged.
- The preload loads at process start, so an app gets the guard at its next
  cold boot after the guest-init release; snapshots taken before keep the old
  behaviour until then.

## Consequences

- Restores of gunicorn apps stop killing a worker on every wake, and the
  slow-connect case above goes away.
- Verified with gunicorn 23: a master and two workers frozen for 40 s, the
  master resumed 2 s before its workers. Without the guard both workers were
  killed and rebooted; with it none were, and requests answered 200.
- uWSGI's harakiri and other servers with their own watchdogs are not
  covered; add them here when one is seen.
