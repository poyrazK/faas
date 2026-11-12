# ADR-20261109140000: Report interpreter processes the restore reseed barrier missed

- Status: Accepted
- Date: 2026-11-09
- Related: ADR-680/687 (restore userspace RNG reseed), ADR-022 amendment
  (resume timing frame), ADR-20261107160000 (snapshot contract for plain apps)

## Context

The ADR-680 barrier reseeds every Node and Python process that loaded the
preload and registered with guest-init. A process that never registered is
not reseeded, and the barrier passes with zero registrations, so nothing
notices. Processes it misses:

- Bun and Deno, which ignore `NODE_OPTIONS`; their `Math.random`,
  `crypto.randomUUID` and buffered crypto state replay across restores;
- Python started with `-S` (no `site`), `-E` or `-I` (ignore `PYTHONPATH`), so
  the `sitecustomize` preload never runs;
- any interpreter a parent starts with a cleared or replaced environment;
- sidecars running from their own root filesystem (documented in ADR-680).

ADR-680 fails closed for processes that registered. Failing closed for the
rest would turn every wake of an affected app into a cold boot, for apps that
may never generate a secret, so the owner chose to warn only.

## Decision

After each barrier pass, guest-init lists running processes whose executable
is `node`/`nodejs`, `python*`/`pypy*`, `bun` or `deno` and that did not
register. It skips:

- Node package-manager launchers (npm, npx, yarn, pnpm, corepack), using the
  same entry-script match as the Node preload, since they deliberately do not
  register;
- processes whose environment sets `GREGALE_RESTORE_RESEED=off`;
- guest-init itself and PID 1.

A process whose root is not `/` is labelled `<runtime>-sidecar`.

guest-init adds the count and the sorted runtime labels to the resume timing
frame (`reseed_unregistered`, `reseed_unregistered_runtimes`) and logs a
warning into the instance's log. vmmd logs a warning naming the instance and
counts `vmmd_restore_reseed_unregistered_total{runtime,scope}` once per
reported runtime per restore. The restore is never refused.

## Consequences

- Operators can see which apps wake with replayable random state, and the
  customer's log says so on each wake.
- The scan reads `/proc` once per restore: a few files per interpreter
  process, well under a millisecond in a guest.
- Older vmmd ignores the new frame fields; older guest-init never sends them.
- Not covered: generator objects inside a registered process (documented in
  ADR-680), runtimes other than these four, and OpenSSL copies bundled into
  native extensions inside a registered Python process, such as
  `psycopg2-binary` or `cryptography` wheels. Python's `ssl.RAND_add` reaches
  only the standard library's libcrypto.

## Validation

- Unit: runtime classification, launcher and opt-out skips, sidecar label,
  de-duplication (`guest/init/reseed_coverage_test.go`); frame round trip
  (`pkg/resumetimingproto`); closed label set and counting
  (`pkg/fcvm/reseed_unregistered_metrics_test.go`).
- Metal, before release: restore a Bun app and a `python -I` app and check the
  warning and the counter; restore a plain Node app and check that neither
  appears.
