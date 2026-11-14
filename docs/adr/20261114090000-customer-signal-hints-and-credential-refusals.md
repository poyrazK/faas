# ADR-20261114090000: Customers see their own attention signals; sign-in refusals are recorded

Status: Accepted

Extends ADR-20261112170000, ADR-20261112210000, ADR-20261113123000 and
ADR-20261113180000.

## Context

Attention signals (ADR-20261113123000, ADR-20261113180000) tell staff that a
customer is probably stuck. The customer is not told: their account health page
lists restrictions, failed deployments, domain problems and refused requests,
but not "your deploys keep failing" or "your app keeps running out of memory".
Both signal ADRs left customer-facing hints as a separate decision.

The refused-request recorder (ADR-20261112170000) runs inside the
authenticated account-handler chain, so it never sees refusals the auth
middleware returns before that chain: a revoked or expired API key or deploy
token, a suspended account, a key limited to other projects. A CI job using a
revoked key is a common "it just stopped working" case, and nothing recorded
it for the account. The stores also dropped the key on those errors, so the
existing `key.expired` and `key.auth_rejected_revoked` audit rows carried an
empty `key_id`.

## Decision

### 1. Customer view of signals

`GET /v1/account/health` and `/dashboard/account/health` gain `signals`: the
account's **open** signals of four kinds, in customer wording, with a next
step. They count towards `attention`.

| Kind | Customer headline | Next step |
| --- | --- | --- |
| `deploy_failing` | Deployments of APP keep failing | Open the app |
| `app_oom` | APP keeps running out of memory | Compare plans |
| `jobs_failing` | Scheduled job NAME keeps failing | `gregale jobs runs NAME` |
| `limit_pressure` | You keep reaching a plan limit | Compare plans |

The wording is built from the signal's kind, subject and observed count
(`attention.ForCustomer`), never from its staff-facing `detail`. A kind without
wording is never shown, so a new kind stays internal until it is given some.
The other kinds stay staff-only: `domain_stuck`, `refused_burst` and
`payment_failing` repeat sections the page already has (custom domains,
refused requests, the past-due restriction), and `boot_failing` includes
platform causes the customer cannot act on.

A signal staff dismissed is hidden from the customer too, so staff can
withdraw a `platform_issue` or `false_positive` signal.

Hints are shown on the page and in the API only; no email or notification is
sent. Sending one needs its own decision on opt-out and rate limits.

The read uses the existing `customer_attention_signals(account_id, state)`
index. When it fails, `signals` is empty and `unavailable` lists `signals`.

### 2. Refusals before the account handler

`pkg/auth/middleware` gains `AccountNote`. apid's authenticated `/v1` chains
(`auth`, `authLimited`, `authVerifiedLimited`) put a note on the request
outside the middleware (`recordCredentialRefusals`). The middleware fills in
the account as soon as a credential resolves to one: an API key, a deploy
token, an OIDC bearer, a platform-tenant token or a session. The inner
recorder marks the note `Reached`. When the request comes back unreached with
a problem code, the outer layer records it for the noted account. Route
pattern, problem code, app slug, request id and trace id are recorded as
before, and the existing per-account budget and retention apply.

`AuthenticateKey` and `AuthenticateDeployToken` now return the key's id and
owning account (`state.refusedKey`) alongside `ErrAPIKeyRevoked` and
`ErrAPIKeyExpired`, with no scopes. Every caller already treats the error as a
refusal. For a deploy token the account is the app owner's. The audit rows now
carry the real `key_id`.

The outer layer sets the note by mutating the request in place, so the
principal the middleware stamps through the same pointer still reaches the
layers outside it.

## Consequences

- A customer sees why something keeps going wrong without waiting for staff.
  Staff wording rules from ADR-20261113180000 ("write only what the customer
  can see") now hold automatically for the four kinds.
- Revoked and expired credentials, suspended-account refusals and project
  limits show up in the customer's refused requests, the 360 diagnosis and the
  `refused_burst` and `limit_pressure` rules.
- Anyone holding a revoked key of an account can add rows to that account's
  refused requests. The account's per-minute budget bounds this, and it shows
  the owner that the old key is still in use.
- Still not recorded: requests with an unknown key or no credentials (no
  account), HTTP status (recording it would mean wrapping the response writer,
  which ADR-20261112170000 rules out to keep streaming and hijacking intact;
  the problem code carries the meaning), and dashboard HTML form errors.
- No migration. Release skew: an apid before this release omits `signals`,
  so the schema leaves it optional. It records no sign-in refusals, which
  matches the old behaviour.
