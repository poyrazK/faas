# FaasCustomerAttentionSpike

**Severity:** warn · **Component:** apid · **Family:** customer_attention

## What it means

apid evaluates customer attention signals every five minutes
(ADR-20261113123000, ADR-20261113180000): repeated failed deployments
(`deploy_failing`), runtime OOM kills (`app_oom`), failed boots
(`boot_failing`), failing scheduled jobs (`jobs_failing`), custom domains
that are not serving (`domain_stuck`), bursts of one refused API problem
code (`refused_burst`), repeated plan-limit refusals (`limit_pressure`) and
unrecovered payment failures (`payment_failing`).
The alert fires when open signals of one kind rose by at least ten within an
hour and stayed there for ten minutes.

Many customers failing the same way at the same time is rarely many
customer mistakes. Treat it as a probable platform cause until shown
otherwise.

## Triage

1. List the signals of the alerting kind:

   ```
   gregalectl accounts attention --kind <kind> --state open
   ```

   or open **Customers who may be stuck** in the staff support inbox
   (`/dashboard/admin/support/attention`).

2. Look for what the signals share:
   - `deploy_failing`: the `detail` column carries the newest failure's
     error code. One code across many accounts points at the builder, a
     base or runner image, or a registry. Check recent releases and the
     builder dashboards.
   - `refused_burst`: the subject is the problem code. Compare
     `apid_customer_api_errors_total` by `code` with the hours before; a code
     that appeared with a release usually means a regression in validation
     or a limit.
   - `app_oom`, `boot_failing`: many apps at once points at a runtime,
     base image or node problem rather than customer code; check the wake
     and node dashboards and recent runtime releases.
   - `jobs_failing`: check the job runner and recent releases of it.
   - `limit_pressure`, `payment_failing`: a jump usually follows a limit
     change or a billing provider incident.
   - `domain_stuck`: the `detail` says whether certificate issuance failed
     (with the authority's error) or DNS drifted. A shared certificate
     authority error points at ACME or rate limits; widespread drift points
     at an edge or DNS change.

3. If a platform cause is confirmed, follow that component's runbook and
   post a status update. Signals resolve on their own once the condition
   clears; do not dismiss them in bulk, and do not contact customers one by
   one about a platform problem.

4. If the signals are unrelated customer issues that happened to cluster,
   leave them for support to contact or dismiss with an outcome.

## Notes

- The gauge is set by whichever apid replica holds the evaluation lock;
  `max by (kind)` covers the others' stale values.
- Thresholds live in `pkg/api/limits.go` (`Attention*`).
