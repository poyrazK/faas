# ADR-1296 · Shared live automation suite history

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Persist bounded, app-scoped live suite selections and verification
  run links. Expose metadata save/read/list APIs, opt-in CLI synchronization, and
  a read-only dashboard history and results view.
- **Why:** CLI suite progress was private to one journal. Teams could inspect
  individual verification runs in the dashboard but could not see their suite
  grouping, unstarted cases or combined current results.
- **Consequences:**
  - `PUT /v1/apps/{slug}/automations/{name}/live-suites/{suite_id}` creates an
    immutable ordered selection or monotonically adds existing run links. Creation
    validates the current saved version, published revision, deployment and all
    selected live cases. Subsequent updates retain the original target and names.
    Linked runs must belong to the same app, automation, case and frozen target.
    Empty links from an older writer cannot erase progress; conflicting run IDs
    or retargeting return conflict. Saving metadata never admits or checks work.
  - GET of the same resource derives results from retained verification records.
    PostgreSQL uses a read-only repeatable-read snapshot; memory uses the store
    lock. Current passing reports and an unchanged suite target are required for
    overall success. Terminal runs without a current report are unavailable,
    pending runs remain pending, and expired records retain their known IDs as
    unavailable. No client-submitted verdict, assertion, key or payload is stored.
  - Collection GET returns the twenty most recently created suite identities and
    linked-run counts, without claiming assertion success. Retention keeps one
    hundred recent suites per automation, independent of workflow-run retention.
    Requests are limited to 16 KiB and 1–32 distinct cases. All limits live in
    `pkg/api/limits.go`; PostgreSQL constrains the stored metadata as well.
    Clearing saved test data or deleting an automation removes its shared suites.
    Actual workflow executions remain governed by existing run retention.
  - Routes use existing account/app authorization, MFA and read scopes; metadata
    PUT requires deploy-write scope and verified email. Responses use no-store.
    API business logic stays in `pkg/apid/automationlive`; apidserver only wires
    scopes and handlers. SQL is generated through SQLC.
  - New CLI suites can opt into `--share`. The choice is retained in their private
    journal and automatically reused on explicit resume. Unshared and existing
    local journals retain their behavior. Shared record UUIDs derive from the
    frozen journal fingerprint, while per-case admission keys remain unchanged
    by sharing. Initial synchronization precedes admission; accepted run IDs are
    persisted locally before synchronization. Sharing failures stop later work
    and keep accepted identities available for recovery through the journal.
    JSON results include `shared_suite_id`; journals and original keys stay private.
  - The dashboard lists shared suites and displays server-derived case outcomes,
    target identities, observation time and links to individual verifications.
    Refresh is a read. Checking assertions, admitting remaining cases and real
    actions remain explicit through the existing verification and CLI flows.
    Shared records do not replace recovery journals or provide publication receipts.
  - Go, Node and Python clients expose the metadata endpoints. Portable tests
    exercise both store implementations, HTTP contracts, dashboard rendering and
    CLI interruption/recovery. They do not qualify guest execution or deployed
    integration credentials. Browser suite execution and release gates remain
    separate milestones.
- **Rejected alternatives:** Uploaded passing reports cannot establish server
  truth. Browser-only groups disappear on navigation. Implicitly checking or
  admitting cases during reads would turn history browsing into a write or a
  real-action trigger. An unrestricted history would permit metadata growth
  without workflow admission limits.
