# ADR-1345 · Product Control customer usage guidance

- **Status:** accepted for internal implementation
- **Date:** 2026-10-16
- **Decision:** Add a shared, read-only current-month allowance projection to the
  customer portal and private customer API behind the existing default-off flag.
  Reuse admission receipts, assignments, live Auth and reviewed plan changes.

## Counts and customer guidance

The portal's **Usage and next steps** uses the `guidance` field of its existing
product response. `GET /v1/product-control/customer/{slug}/guidance` returns the
same projection without Operation history, onboarding observation writes, billing
amounts, prices, execution bindings, inputs or results. It accepts no query
selectors; account, app and tenant come from the owned product, live Auth session
and selected mapped organization. It is excluded from public OpenAPI and SDKs.

An allowance includes every accepted start for that feature in the current UTC
admission month, across all assigned plans and catalog revisions. Failed,
cancelled and pending work still consumes a start. A plan change carries those
starts; a downgrade can leave usage above the assigned limit. Remaining starts
clamp to zero, while the actual used count remains visible. No counter, quota
policy, storage schema or admission transaction changes.

Warnings begin at 80% of a positive monthly allowance. The next period begins at
00:00:00 UTC on the first day of the next month, including year and leap-year
boundaries. A zero grant says that the plan includes no starts; waiting for the
monthly reset does not make that feature runnable.

| Feature state | Customer guidance |
| --- | --- |
| `available` | Remaining allowance; availability is checked again at confirmation |
| `near_limit` | At least 80% used; review an authorized next step |
| `allowance_exhausted` | Positive allowance used up, with exact reset; separate explanation for a zero grant |
| `access_paused` | Workspace's new runs are paused; contact the product team |
| `permission_required` | Current role cannot run the feature; ask an administrator |
| `temporarily_unavailable` | Assigned feature has no usable current deployment binding; refresh or contact the product team |

Paused access, permission and binding availability take precedence over the
allowance warning. The separate `allowance_state` retains available, near-limit,
exhausted or no-allowance status without implying that a plan change repairs a
permission, suspension or deployment issue.

## Authority and existing actions

Every request rechecks the live Auth organization membership, current Product
Control access policy, account/app ownership and active tenant mapping. Caller
role claims, owner API keys and platform service credentials cannot replace the
customer session. The projection returns feature counts only with Run work or
View billing. Other permitted roles receive the scoped period/status metadata
and an empty feature list. Run-only users receive no financial usage lines.

A plan action offers **Review available plan changes** only with Manage plan,
an unpaused customer and a transition explicitly offered by the current catalog.
The portal displays that action only for near-limit or exhausted features. The
button reloads the existing reviewed choices; it never previews, confirms,
starts work or changes a plan automatically. Current choices, terms, authority
and explicit confirmation remain enforced by the existing plan flow. A choice
is not a promise of a higher allowance. Other users receive administrator or
product-team guidance. No arbitrary destination URL is returned.

## Observation, bounds and recovery

The shared service read has a twenty-second deadline and at most the existing
32 assigned features. It checks the assigned customer and catalog head again
before returning, checks the live permission revision/realm, and rejects reads
that cross the UTC month or observe changed assignment/policy. Read errors fail
the response. These sequential observations are not one database snapshot and
reserve no starts; concurrent usage or deployment changes can still occur.
Admission and plan confirmation continue to enforce their own current policy.

Historical product reads omit current-month guidance. The browser validates
scope, revision, period/reset and remaining counts against its accompanying
product response; malformed or missing guidance shows an unavailable message.
It ignores older refresh responses, clears guidance after a failed current
refresh, and clears workspace data on logout or switching. Stale action buttons
cannot dispatch for a newer view. Existing uncertain-submission recovery remains
available even when the current allowance is exhausted; guidance never replaces
or retries an unknown run.

Private no-store headers apply to responses and assets. The pre-mux surface gate
covers customer APIs/assets, tenant-self product routes and owner surfaces, so
disabled routes return 404 even for unsupported methods. Normal Operations
routes retain their existing policy.

Portable domain, HTTP and browser checks cover thresholds, UTC boundaries,
counts across revisions, role redaction, workspace isolation, Auth revocation,
stale reads, reviewed actions and disabled methods. Local Chromium checks use
fictional product data. This milestone enables no flag and does not replace the
deployed Auth/email/browser/native-export pilot.
