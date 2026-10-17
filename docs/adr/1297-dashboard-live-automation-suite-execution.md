# ADR-1297 · Durable dashboard live automation suites

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Execute an explicitly confirmed ordered selection of saved live
  cases from the dashboard. Keep progress on the server and atomically commit
  each workflow admission together with its immutable shared-suite run link.
- **Why:** Shared history made CLI suites visible to teams, but multi-case
  execution and recovery still required a private CLI journal.
- **Consequences:**
  - Start binds the saved automation version, published revision, deployment and
    1–32 distinct saved live case names. All selected cases are validated before
    the first admission. New suite IDs are UUIDv7 with a timestamp within the
    previous ten minutes. Retained identical start requests read the original
    progress, including during a runtime outage; they never advance later cases.
  - POST `live-suites/{suite_id}/advance` requires real-action confirmation on
    every request. It checks existing original runs in order and admits at most
    one next case after all earlier results pass. Failed, missing, outdated,
    cancelled and nonterminal runs stop advancement. No replacement run, workflow
    resume, cancellation, target refresh or assertion override is introduced.
  - Both stores serialize admission with existing workflow writers. PostgreSQL
    locks the app admission boundary, account/app/deployment target, saved
    definition and checked workflow states. The new run and suite link commit
    together. A lost HTTP acknowledgement has a retained identity to recover;
    concurrent tabs cannot race two admissions for the same suite position.
  - Managed records reject metadata PUT. CLI sharing remains metadata-only and
    cannot be taken over by dashboard continuation. Individual run retention is
    independent: a missing linked verification remains unavailable, with its
    known run ID, and never causes readmission.
  - History still retains at most 100 suites per automation and lists 20. Recent
    managed start receipts are prioritized during pruning until their creation
    window expires. Creation fails before admission if all slots are protected.
    A missing expired UUIDv7 cannot recreate its execution after history pruning.
    Clearing saved test data or deleting the automation removes shared history;
    old continuation requests then fail rather than creating replacement suites.
  - API writes use existing account/app authorization, MFA, deploy-write scope
    and verified email. Strict bounded JSON rejects extra fields. Dashboard
    continuation additionally requires its named CSRF envelope and a narrow form
    containing only confirmation and the CSRF token. Store work has a ten-second
    request deadline. Responses expose identities and safe assertion summaries.
  - The browser advances only after a confirmed start or continuation. GET,
    refresh, history links and query parameters cannot enable execution. The
    loop is bounded to 100 requests at three-second intervals, with a ten-second
    request timeout. Hiding or leaving the page, stopping, errors or an unresolved
    result stop further advancement. Already admitted workflows run independently.
    Reopening requires explicit continuation of the same suite. JavaScript-free
    forms advance one bounded step per confirmed submission.
  - Go, Node and Python clients expose start and advance. Tests cover real memory
    and PostgreSQL persistence, concurrent advancement, missing records, protected
    history, rollback on identity conflict, HTTP contracts, dashboard authorization
    and browser progress behavior. They do not qualify native guest execution or
    external integration credentials.
  - Node generation encodes named automation path values as individual segments,
    matching the Go and Python clients, while preserving existing encoding for
    other route families and explicit client encoder configuration.
- **Rejected alternatives:** Browser journals lose recovery ownership on close.
  Separate admission and metadata transactions create uncertain execution gaps.
  Background admission or action-bearing GET requests weaken explicit recovery
  and confirmation. Unlimited receipt retention creates unbounded metadata.
