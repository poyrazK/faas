# ADR 20261028144601 · Production e2e findings: readiness refusal reason, base review wait, scan reuse, deploy-token CLI

- **Status:** proposed
- **Date:** 2026-10-28
- **Amends:** ADR-138 (container lifecycle contract), its readiness-failure
  reporting; ADR-075 (per-deploy scan), which scanned every deployment
- **Extends:** ADR-20261027090010, whose shared-base scan bound
  (`api.ApplicationStandardBaseScanTimeout`) now also bounds the wait for a
  busy review
- **Related:** issue #5709 (customer journey suite against production,
  rc.296/rc.297), ADR-20261027173012 (the first round of #5709 fixes),
  ADR-373 (DNS-gated egress), ADR-031 (egress allowlist)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-20261027173012 fixed the traffic and capacity findings of #5709. Five
more had a code or documentation cause:

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
3. **Cache-hit deploys still scan for half a minute.** On production a
   build-cache-hit deploy of a 3-file Node 22 app spent 21–34 s in the
   per-deploy scan: about 7 s hashing the 386 MB artifact and 14–25 s in
   Grype, which repeated the previous deployment's answer for the same bytes.
   The rest of the 3.3-minute deploy is the artifact's size (the musl Node 22
   runner never shares the runtime base) and the builder slot queue, which
   `source_download` includes.
4. **Deploy tokens had no CLI command.** The capability is GA; only the API
   and dashboard reached it.
5. **"Apps cannot open public HTTPS connections."** The suite dialed
   `1.1.1.1:443`. ADR-373 refuses new flows to addresses the guest did not
   resolve through the bridge resolver, and a non-empty ADR-031 allowlist
   refuses everything it does not list, but the customer networking page
   described neither, so the suite read ADR-031 as "every public address".

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
4. **imaged reuses a scan of an unchanged artifact.** For an app whose
   security policy is not `enforce`, the per-deploy scan reuses the result of
   one of the app's 20 most recent deployments when its artifact digest
   matches and it came from the scanner version and vulnerability database
   (version, build time, valid status) of the last real scan on that node,
   run within the hour. The result names the deployment it repeats
   (`reused_from`) and keeps its original `scanned_at`. A failed scan, a
   restart or an hour without a scan turns reuse off until the next real
   scan. Enforce-mode apps always scan, so their evidence stays this
   deployment's own. The live re-scan keeps re-evaluating live deployments
   as the database moves on.
5. **`gregale app deploy-token list|create|rotate|delete --app SLUG`** (also
   `gregale deploy-tokens list|add|rotate|rm`). The commands call the
   existing routes. Minting and rotating keep the API's 5-minute MFA step-up;
   the help names `gregale account mfa verify`.
6. **The networking page documents outbound rules:** ports 80 and 443 plus a
   plan's extra ports, connect by hostname, what an allowlist changes, and
   what is always refused. The journey probe (`tests/journeys`, not yet on
   main) passes when any public target connects and dials `api.github.com`
   beside the raw addresses.

## Consequences

- A release refused by its health check fails with the health-check reason,
  whichever writer reaches the row first.
- A deploy behind a concurrent base scan waits up to 20 minutes on that scan
  instead of failing, and a node no longer keeps refusing boots because one
  deploy gave up.
- CI users can mint and rotate deploy tokens from the CLI.
- A redeploy of unchanged code on a node that scanned within the hour skips
  Grype (14–25 s on production). It still hashes the artifact, and a
  reused result can be up to an hour behind a database update the node has
  not yet scanned with; the live re-scan covers that window.
- Not changed here: the artifact size behind the slow `image_build` stage
  (sharing a Node 22 runtime base) and the builder slot queue.

## Verification

- `pkg/fcvm`: `TestReadinessCutShortKeepsTheHealthcheckReason`,
  `TestWaitReadyAnswersBeforeTheCallerDeadline`.
- `pkg/sched`: `TestMarkPrimeFailedRefinesTheWatchdogTimeout`.
- `pkg/imaged`: `TestProducedBaseScanWaitsForABusyReview`,
  `TestDeployScanReusesAnUnchangedArtifactsResult`.
- `cmd/gregale`: `TestDeployTokensReachEachRoute`,
  `TestDeployTokensRejectBadArgumentsWithoutACall`,
  `TestDocsSpellCommandsInTheTree`, `TestMarkdownReferenceFresh`.
