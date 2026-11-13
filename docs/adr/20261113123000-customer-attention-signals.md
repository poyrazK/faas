# ADR-20261113123000: Customer attention signals

Status: Accepted

## Context

Support learns that a customer is stuck only when the customer writes in.
The account lookup, 360 diagnosis, recorded API errors and account health
page (ADR-20261112210000) show what happened once staff look, but nothing
tells staff where to look. Customers who give up after repeated failed
deployments, a wall of refusals or a custom domain that never serves often
never open a ticket.

## Decision

apid evaluates three rules every `AttentionEvaluateInterval` (5 minutes) and
keeps the result in `customer_attention_signals`:

| Kind | Opens when | Subject |
| --- | --- | --- |
| `deploy_failing` | ≥ `AttentionDeployFailures` (3) failed deployments of one app within `AttentionDeployWindow` (24 h) and no successful deployment of it since the first of them | app slug |
| `refused_burst` | ≥ `AttentionRefusedBurst` (20) recorded customer API errors with one problem code within `AttentionRefusedWindow` (1 h); `not_found` is ignored | problem code |
| `domain_stuck` | a custom domain whose certificate failed more than `AttentionDomainFailedAfter` (1 h) ago, whose DNS drifted, or that stayed unverified for `AttentionDomainUnverifiedAfter` (24 h) | hostname |

The evaluator runs in one transaction under a transaction-scoped advisory
lock, so with several apid replicas exactly one evaluates per tick. A rule
match upserts the active row (open or dismissed) for (account, kind,
subject), refreshing `detail`, `observed` and `last_seen_at`. An active row
that no rule matched in this pass is resolved. Resolved rows are pruned after
`AttentionResolvedRetention` (30 days). A partial unique index keeps at most
one active row per (account, kind, subject).

Staff can dismiss an open signal with a reason (≤ `AttentionDismissReasonMax`
bytes). A dismissed signal stays quiet while the condition persists and
resolves like any other once it clears; a later recurrence opens a new row.
Dismissing needs a support staff role, CSRF, a same-origin request, a session
principal and a five-minute step-up, like other support portal writes.

Signals surface in three internal places only:

- `GET /v1/admin/obs/attention` (operator session read; `kind`, `state`
  `active|open|dismissed|resolved`, `account_id`, `limit` ≤ `AttentionListMax`)
  and `gregalectl accounts attention`;
- the staff page `/dashboard/admin/support/attention`;
- `attention_signals` in the tenant 360 diagnosis and the ticket account panel.

v1 is internal-only. Customers are not notified and no outreach is sent;
staff decide whether to contact a customer.

apid exports `apid_customer_attention_signals_open{kind}` and
`apid_customer_api_errors_total{code}` (counted for every customer problem
response with a valid code, whether or not the recorder sink is configured).
`FaasCustomerAttentionSpike` warns when open signals of one kind rise by ten
or more within an hour, which usually means a platform cause.

## Consequences

- Support can contact stuck customers before they write in, and a cluster of
  identical signals points at a platform regression early.
- Thresholds are constants in `pkg/api/limits.go`; changing them needs a
  release. Per-plan or per-account thresholds are out of scope.
- The evaluator reads `deployments`, `customer_api_errors` and `domains`
  every five minutes. The queries are bounded by their windows and existing
  indexes; the cost is one short transaction per tick across the fleet.
- Customer-visible outreach (emails, in-dashboard hints) needs a separate
  decision about wording, opt-out and rate limits.
