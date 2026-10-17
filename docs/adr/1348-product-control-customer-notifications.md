# ADR-1348 · Product Control Customer Notifications

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Add an Updates inbox to the existing customer portal behind the
  default-off Product Control flag, with durable workspace-shared read state.

## Sources and visibility

The inbox projects the latest retained state of each source within a rolling
thirty-day window. Work notifications use independent Product Control receipt
outcomes (`succeeded`, `failed`, `cancelled`), so result expiry does not remove
that evidence. An unresolved receipt whose retained, unexpired Operation requires
reconciliation produces a needs-attention update. Help requests produce their
latest owner response/status after version one. Initial help submission is not
an owner update. Each source occupies one item; intermediate changes coalesce.

This is an observation of current retained sources, not a delivery journal,
complete event history or promise of email delivery. Help retains only its latest
response. Attention can disappear after reconciliation, result expiry or the
window boundary. Work outcomes retain their original settlement time. All times
and window comparisons use UTC, with PostgreSQL microsecond precision.

Each request uses the existing live Application Auth session and verified
organization mapping. Metadata writes additionally require the matching HTTPS
Origin under the existing customer middleware. The storage transaction rechecks
current ownership, active tenant, Auth app identity, mapping and role policy. Work requires View work;
workspace help requires any product permission, feature help requires Run work
or View billing, and run help requires View work. Filtering applies before
pagination and unread counts, and direct acknowledgements repeat these checks.
An owner or service key cannot replace a customer session. A client-supplied role
or tenant selector grants no authority.

Help sources and read metadata retain Auth app/organization identity. A remapped
organization cannot inherit old help or read state. Retained work follows the
current tenant's existing work visibility and starts unread for a new mapping;
it does not acquire a person-level author identity. Auth membership and platform
state remain separate observations; subsequent requests enforce revocation.

## Shared read state and concurrent choices

The existing customer boundary retains workspace authority, not a person's ID.
Read status is therefore explicitly shared by all currently authorized members
of that workspace. The UI and API identify this scope. Reading a source or
opening a link never acknowledges it implicitly.

An item has a stable `help/<UUID>` or `work/<UUID>` identity and a revision digest
bound to account, app, tenant, Auth realm, organization and the current source
version. Help versions use the retained edit version; work versions use state
and occurrence time. Read metadata records that version, desired state and a
monotonic read version. A newer source version becomes unread.

Acknowledgement requires the current revision, explicit desired boolean and
expected read version. A stale source revision conflicts even if the desired
state appears to match. An already matching current state returns idempotently;
otherwise read versions must match before incrementing. Retrying a lost old
acknowledgement cannot undo a later read/unread choice or hide a newer reply.
The existing account transaction lock serializes PostgreSQL read choices and
capacity checks; memory uses its mutex. Execution rows are not locked or mutated.
A concurrently changing source can leave an acknowledgement for the observed
old version; projection still shows the newer version unread.

## Storage and bounds

The append-only migration adds `product_control_notification_reads`, with owned
account/app/tenant cascading foreign keys and retained realm, organization and
source identity. Metadata expires at the acknowledged source time plus thirty
days. List queries ignore expired metadata; metadata writes prune expired rows
under the account lock. Expiry bounds visibility and active capacity; it is not
a scheduled physical-deletion guarantee.

At most one thousand active marker rows are allowed per product tenant across
realms. Existing marker changes and matching retries remain possible at capacity;
new markers return the existing bounded quota error. Read versions are at most
one million. These limits, twenty-item pages, 4 KiB bodies, 128 KiB responses and
twenty-second deadlines live in `pkg/api/limits.go`. Production SQL uses SQLC.

Pages sort by occurrence time and canonical source ID descending. A scoped
filter-bound cursor with one-row lookahead bounds each response. Unread counts
cover all currently visible sources in the window, independent of page/filter.
Pagination is a live observation, not a frozen cross-page snapshot. Receipts can
outlive result retention, so scanning/counting retained source metadata still
depends on tenant history size; no constant-cost claim is made.

## Portal and internal boundary

Updates offers All/Unread, explicit read/unread buttons, refresh, older pages,
and links to existing run/help readers. First-page polling discovers changes;
older pages remain stable until explicit navigation. Uncertain acknowledgements
refresh by GET without replaying a POST. Logout, workspace change and revoked
authority clear private data and fence pending responses. Text renders through
`textContent`, and existing CSP and same-origin cookies remain in place.

Routes and the new static asset use the existing pre-mux default-off gate and
private/no-store responses. The API is internal and excluded from public SDK
and OpenAPI coverage. Inbox actions change only read metadata: there is no
execution, repair, price, billing, plan or permission change. Email delivery and
the deployed three-customer Auth/browser/native-export pilot remain separate
work; this milestone does not enable the flag or deploy the service.
