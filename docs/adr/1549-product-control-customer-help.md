# ADR-1549 · Product Control Customer Help

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Add customer-initiated help requests, scoped diagnostic snapshots,
  an owner review queue and customer-visible request status behind the existing
  default-off Product Control flag. Operational repairs use existing review flows.

## Workspace requests and authority

Customers can report a workspace question, an assigned feature or a retained
Product Control run. Requests belong to the account, product, platform tenant,
Auth app identity and Auth organization verified by the live customer middleware.
Request bodies cannot select those identities or supply diagnostic context.
Owner keys and service credentials cannot substitute for customer sessions.

Requests are shared within the verified workspace, subject to their context's
current visibility class:

| Kind | Required current Product Control permissions |
| --- | --- |
| `workspace` | Any currently granted product permission |
| `feature` | Run work or View billing |
| `run` | View work |

These checks apply to creation, direct lookup, pagination and idempotent recovery.
Members who lose the relevant permission cannot read the request or its text.
Different permitted members of the same workspace can follow a request. The
portal makes this sharing explicit; customer-authored messages and owner
responses are workspace-visible text, not an internal/private staff channel.

Every request rechecks live Auth membership and current product grants. Store
transactions recheck owned active account/app, active tenant, current Auth realm,
organization mapping and current role policy. Changing an organization's mapping
or recreating/changing its Auth app does not give the new identity access to old
requests. Owners retain scoped historical access. No person's identity is
invented: the existing Auth boundary supplies workspace authority, not a retained
person-level help author identity. Auth and platform transactions are separate
observations; revocation across that boundary is enforced on subsequent requests.

## Context and request lifecycle

The server captures a bounded observation when it accepts a new request:

- Workspace questions retain assigned plan/revision, policy revision, UTC period,
  observation time and paused status.
- Feature requests add one assigned feature's existing usage-guidance projection:
  accepted starts across revisions, assigned limit, remaining starts, reason and
  next UTC reset. Binding availability is observed only with Run work permission.
- Run requests validate the independent Product Control admission receipt against
  the account/app/tenant and retain the run reference, feature and original
  plan/revision/admission month. This works independently of result retention.

Prices, billing amounts, execution bindings, inputs, outputs, credentials, Auth
organization identifiers and invitation tokens are not attached. A snapshot
does not reserve starts, establish current health or authorize another action.
Creating or reviewing help changes no work, quotas, assignments or charges.

The initial state is `open`, with version one. Owners can set `open`,
`in_progress` or `resolved` and a bounded customer-visible response. The initial
customer message and diagnostic context remain immutable; this release retains
the latest response/status, not a conversation or complete edit journal.
Resolving a request does not assert that a run succeeded or a feature was repaired.
Owner pages link to existing customer activity, readiness and run review pages.
Those workflows independently enforce their own reviews and authorization.

Creation uses a customer-generated canonical UUID and exact-body fingerprint.
An accepted retry returns the existing request's current status and original
context before checking quotas or recapturing transient diagnostics. Changed
details under the same ID conflict. Owner updates compare the expected version;
competing edits cannot overwrite one another. A retry whose desired status and
response already match only reads the current value. An older retry cannot
restore a previous response after another edit.

## Persistence and bounds

`product_control_help_requests` stores immutable creation/context and mutable
status, latest response, version and timestamps. Account/app/tenant deletion
cascades PostgreSQL rows. Memory returns detached values and hides removed
owners/tenants. No automatic expiry is introduced. SQL is generated through SQLC.

Per workspace, at most twenty requests can remain open/in progress and at most
two hundred can be retained. Both stores serialize quota checks, creation and
updates with the existing account/app/tenant ordering. PostgreSQL uses the
existing account transaction lock; memory uses its store mutex. Exact accepted
retries remain recoverable at capacity. No execution/code-row locks are acquired.
Versions are bounded at one thousand. Customer messages and responses each have
a 2,000-byte UTF-8 bound; bodies are at most 16 KiB, pages twenty requests,
responses 256 KiB and service calls twenty seconds. Limits live in `pkg/api/limits.go`.

Pages order by immutable creation time and canonical UUID descending, with a
scope/filter-bound ordering cursor and one-row lookahead. Customer query filters
apply realm, organization and current permissions before the page bound. Owners
can filter by tenant and state, including active or all requests. Cursor contents
are not authorization. Refresh discovers newer requests and current statuses;
pagination is not a frozen cross-request snapshot.

## Portal, owner review and recovery

The portal links blocked features and retained work to the Help form, without
sending a request until the customer submits it. Context is presented as an
observation at submission, with plain language status and the latest response.
The owner queue and request page render text through escaped templates, use
normal MFA/owner scopes, and require verified email and authenticated CSRF for
browser status updates. JSON updates use the existing owner write boundary.

Browser recovery stores only a scoped request UUID and input hash, never the
message, response, diagnostics or credentials. Web Locks serialize submissions
across tabs. Reload checks the saved reference by GET; it never resubmits. If
unconfirmed, explicit retries retain the original ID and require original details.
Logout, workspace switching and lost authority clear private UI and fence pending
reads/writes. Older history responses cannot replace a newer observation.

All new APIs/assets and owner pages use the existing default-off pre-mux gate and
private/no-store responses. The private API remains excluded from public OpenAPI
and generated SDK contracts. There is no external email delivery, impersonation,
automatic retry/repair or new permission grant in this milestone. The deployed
internal Auth/email/browser/native-export pilot remains a separate release gate.
