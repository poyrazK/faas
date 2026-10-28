# ADR 20261028144601 · Production e2e findings: readiness refusal reason, base review wait, deploy-token CLI

- **Status:** proposed
- **Date:** 2026-10-28
- **Amends:** ADR-138 (container lifecycle contract), its readiness-failure
  reporting
- **Extends:** ADR-20261027090010, whose shared-base scan bound
  (`api.ApplicationStandardBaseScanTimeout`) now also bounds the wait for a
  busy review
- **Related:** issue #5709 (customer journey suite against production,
  rc.296/rc.297), ADR-20261027173012 (the first round of #5709 fixes),
  ADR-075 (per-deploy scan)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-20261027173012 fixed the traffic and capacity findings of #5709. Three
more had a code cause:

1. **A release refused by its health check reports `cold_boot_timeout`.**
   A release whose `/healthz` answered 503 failed as
   `app_startup_timeout: cold_boot_timeout: instance=…`, or with
   "snapshot prime … context canceled". vmmd's readiness window starts after
   the guest boots and can outlast the scheduler's cold-boot budget (ADR-138
   keeps prime budgets at 30 s). The schedd watchdog then stopped the guest
   and failed the deployment first; vmmd's wait ended on the cancelled
   context and returned `context canceled`, and `markPrimeFailed` would not
   overwrite the terminal row anyway. The typed readiness problem, naming
   the path and the status it answered, was lost.
2. **Concurrent deploys fail on the base review.** Publishing the produced
   shared-base scan sidecar gave up after 30 s when another deploy owned the
   application standard review inputs ("review inputs are being modified").
   That owner can be scanning the same base, which takes minutes. Giving up
   left the fail-closed sidecar in place: on 2026-10-27 fsn-2's imaged failed
   at 17:57 and vmmd refused 62 cold boots with the placeholder until 21:16,
   which the suite saw as wake 503s and `vmm_boot_failed`.
3. **Deploy tokens had no CLI command.** The capability is GA; only the API
   and dashboard reached it.

## Decision

1. **vmmd answers before the caller's deadline.** When the caller's context
   has a deadline more than two seconds away, the HTTP readiness wait ends
   one second before it, so its typed `healthcheck_not_ready` problem reaches
   schedd before schedd stops waiting. When the wait is cancelled instead
   (the watchdog stopped the guest) and the app had already answered the
   health check without a 2xx, or without HTTP, vmmd still returns the typed
   problem, with the time waited. A wait with no answer at all still returns
   the context error.
2. **schedd lets that reason replace its own generic timeout.**
   `markPrimeFailed` overwrites a failed deployment only when the row holds
   the watchdog's cold-boot timeout (`app_startup_timeout`, error starting
   `cold_boot_timeout: `) and the prime failure is a typed problem with a
   code. Every other terminal or live row stays untouched.
3. **imaged waits for the base review owner.** Publishing the produced
   shared-base scan retries while the review inputs are busy, for up to
   `api.ApplicationStandardBaseScanTimeout` (the base scan's own bound), with
   backoff from 250 ms to 5 s. Each attempt reuses the owner's result once it
   is published.
4. **`gregale deploy-tokens list|add|rotate|rm --app SLUG`.** The commands
   call the existing routes. Minting and rotating keep the API's 5-minute MFA
   step-up; the help names `gregale mfa verify`.

## Consequences

- A release refused by its health check fails with the health-check reason,
  whichever writer reaches the row first.
- A deploy behind a concurrent base scan waits up to 20 minutes on that scan
  instead of failing, and a node no longer keeps refusing boots because one
  deploy gave up.
- CI users can mint and rotate deploy tokens from the CLI.
- Not changed here, measured on production for #5709 item 7: a cache-hit
  deploy's `image_build` and `security_scan` stages track the size of the app
  layer (386 MB for a 3-file Node 22 app, because the musl Node runner never
  shares the runtime base) and `source_download` includes the builder slot
  queue. Reusing a scan result for an identical artifact digest, scanner and
  database would remove 14–25 s of Grype time per repeat deploy and needs its
  own ADR-075 amendment.

## Verification

- `pkg/fcvm`: `TestReadinessCutShortKeepsTheHealthcheckReason`,
  `TestWaitReadyAnswersBeforeTheCallerDeadline`.
- `pkg/sched`: `TestMarkPrimeFailedRefinesTheWatchdogTimeout`.
- `pkg/imaged`: `TestProducedBaseScanWaitsForABusyReview`.
- `cmd/gregale`: `TestDeployTokensReachEachRoute`,
  `TestDeployTokensRejectBadArgumentsWithoutACall`.
