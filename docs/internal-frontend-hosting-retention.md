# Automatic cleanup of internal hosting deployments

This default-off extension retires abandoned uploads and expired, never-published
CI releases. It uses the existing publication, grace-period and artifact cleanup
controllers. See [ADR-202610171001](adr/202610171001-opt-in-hosting-retention.md).

## Opt in deliberately

Drain hosting API writes, apply `20261017100104546_hosting_retention_policy.sql`
and upgrade **every** API writer. The migration gives existing releases a fresh
idle window. New writers record upload/verification activity even when the
retention flag is off; old writers do not. Drain old in-flight requests before
opting in. No Worker update, Terraform apply or Cloudflare plan change is needed.

| Surface | Separate opt-in, default off |
| --- | --- |
| API and automatic worker | Runtime `frontend_hosting_retention_enabled`, bootstrapped by `FAAS_FRONTEND_HOSTING_RETENTION_ENABLED=1` |
| Per site | An explicitly enabled retention policy, saved with its reviewed policy revision |
| CLI workstation | `FAAS_FRONTEND_HOSTING_RETENTION_ENABLED=1`, plus the main hosting workstation flag |
| Dashboard build | `NEXT_PUBLIC_FRONTEND_HOSTING_RETENTION_ENABLED=true`, plus main hosting and advertised API capability |

Terraform's apid environment leaves the new flag at `0`. Sites without a policy
remain disabled with revision `0`. Existing main hosting, active account,
operator allowlist, scope, MFA and ownership checks remain authoritative. The
worker checks current account/operator authorization and configured origin.
Automation credentials cannot read cleanup plans or change policies. Cleanup
continues to protect preview references and CI windows even when their admission
flags are off.

## Preview rules and set a policy

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_RETENTION_ENABLED=1
gregale sites cleanup --site SITE_UUID
gregale sites cleanup --site SITE_UUID --pending-age 24h --staged-age 24h
gregale sites retention show --site SITE_UUID
gregale sites retention set --site SITE_UUID --enabled=true \
  --pending-age 24h --staged-age 24h --expected-revision 0
```

`cleanup` is read-only. Both proposed ages must be supplied together; otherwise it
uses saved rules. Output includes stored `policy`, evaluated `rules`, `proposed`,
`evaluated_at`, per-release `entries`, `reclaimable_reserved_bytes` and
`retiring_reserved_bytes`. Policy revisions are separate from site revisions;
review the policy's revision before saving. Ages default to 24 hours and must be
whole seconds between one hour and 30 days. Setting a policy requires explicit
`--enabled`, both age flags and the reviewed revision; omitted age flags never
reset existing rules. They are central limits advertised
by capabilities. No policy is inferred from a quota error or CI credential.

The dashboard's **Automatic cleanup** panel shows these same catalog observations.
Edit the age rules and enabled checkbox, choose **Preview cleanup rules**, then
**Save reviewed policy**. Saving confirms that automatic retirement applies to
future eligible releases too. A changed policy revision, edited rules, failed
refresh or ambiguous save requires another review. It never automatically
retries an uncertain policy write with a newer revision. A saved policy may
immediately select already-idle eligible releases on the next pass.

API routes:

- `GET /v1/internal/hosting/sites/{site}/cleanup`: current policy and read-only plan.
  Optional `pending_age_seconds` and `staged_age_seconds` query values evaluate
  proposed rules without storing them; specify each exactly once.
- `PUT /v1/internal/hosting/sites/{site}/retention`: save
  `{enabled, expected_revision, pending_age_seconds, staged_age_seconds}`.
  Enabled and expected revision must be explicit. A stale revision returns 409;
  inspect the current plan instead of silently resubmitting.

There is no execute-plan endpoint and no change to the existing manual retirement
flow. A displayed plan does not authorize deletion; the background worker reads
current state under the site lock before accepting retirement.

## Eligibility and protection

| Release/dependency | Automatic behavior |
| --- | --- |
| Pending upload | Eligible after no upload-grant/verification activity for its pending age |
| Ready release from CI | Eligible after staged idle age and the original CI preview/retry expiry |
| Active or ever-published release | Protected, including rollback targets and retained assets |
| Ready manual staging | Protected for explicit operator review/retirement |
| Remaining preview reference | Protected, including expired/revoked previews awaiting safe cleanup |
| CI retry window still open | Protected, even if its upload looks idle |
| Recent or unknown activity | Protected |
| Already retiring | Shows publication wait, grace period or artifact cleanup stage |
| Leased/deleting site | No new automatic retirement |

Upload grants and verification refresh last activity under the same site lock
used by retention. The one-hour minimum is longer than the existing bounded CI
workflow, signed grant lifetime and verification work. A client should renew
grants and retry within the original CI window; an expired window cannot be
extended by rerunning. Recent activity invalidates an older cleanup observation.
A new preview or promotion accepted first blocks retirement; retirement accepted
first prevents those later operations on that release.

All preview rows hold bytes until normal revocation/tombstone acknowledgment and
removal grace complete. Expiry alone is insufficient for deleting their files.
Previously promoted releases are never selected in this version, so automatic
retention cannot break retained published-asset fallback or discard rollback
versions. Manual ready releases are not inferred to be CI from source labels.

## Cleanup, quotas and failures

The worker starts with the existing hosting controllers and checks the main and
retention flags before each site. Every pass inspects at most 100 opted-in sites,
with a 30-second context deadline, continuing its keyset cursor on the next pass.
An individual failure cannot starve later sites. Multiple API workers may inspect
the same site; site locks and current-state checks make retirement idempotent.

Retirement commits its outbox updates transactionally. Provider publication must
be acknowledged, and the existing retirement grace must pass before artifact
removal. Failed removal retains the catalog/storage reservation and retries.
`reclaimable_reserved_bytes` means eligible catalog reservations, not physical R2
inventory or an immediate capacity refund. Pending slots stop counting at accepted
retirement; retained-release count and storage remain occupied until safe cleanup.
The UI marks already-retiring reservations separately.

Policies do not guarantee unlimited CI throughput. The existing 10 releases/site,
4 pending uploads/account, preview limits and account storage cap remain. Defaults
of 24-hour idle ages and 24-hour CI previews can retain an entire day's work; tune
preview TTL and idle ages deliberately within their limits. Published and manual
staged releases still need operator lifecycle management.

CI run identities survive retirement and physical release/preview cleanup. Late
retries return the original IDs and cannot recreate removed resources. Retention
does not prune or reset the existing **1,000 run identities/site** pilot limit.
It adds a small policy row/site and a release activity timestamp, no per-site
process, extra edge lookup, artifact copy or build runtime.

To stop future automatic retirement, disable the runtime flag or save
`--enabled=false` with the current reviewed policy revision. In-flight accepted
retirement and the independent existing publication/GC controllers continue;
already-retired releases are not restored. Disabling the flag does not affect
serving. Policy changes and accepted retirement batches emit identifier-only
audit events. Worker failures use generic logs without provider credentials.

Downgrade refuses while any policy remains enabled. For a planned rollback,
disable each policy while management is enabled, then disable the runtime flag
and drain workers. Do not re-enable automatic retirement just to unblock a schema
downgrade; coordinate offline maintenance separately if it was already stopped.
Disabled policy settings are removed by downgrade; archive them if needed. Drain all
retention workers and hosting writers, and coordinate the normal all-reader
upgrade/rollback procedure before removing the activity column. This is separate
from deleting release bytes. Local evidence covers memory/Postgres races and
rollback, migration backfill, activity renewal, authorization, publication/removal
failure recovery, CLI review and dashboard stale-state handling. Live provider
qualification remains deferred.
