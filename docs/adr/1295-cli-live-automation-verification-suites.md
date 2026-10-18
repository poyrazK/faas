# ADR-1295 · Resumable CLI live automation verification suites

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Add `gregale automations live suite` over the existing live
  verification APIs. Execute an explicitly ordered selection of saved cases
  sequentially, retain private resumable progress and emit one safe CI report.
- **Why:** Deployment pipelines need to verify several real cases together and
  recover interrupted observation without losing the original admission identity.
- **Consequences:**
  - New suites require 1–32 distinct saved cases, saved automation version,
    published revision, deployment UUID, stable suite key, new journal path and
    real-action confirmation. The plan also binds the app UUID and API endpoint
    hash. All selected cases must have valid live expectations for the published
    definition before execution starts. Every admission retains the original
    server-enforced version/deployment fences; target changes never refresh them.
  - SHA-256 case keys bind the endpoint, app identity, automation, suite key,
    frozen target and case. Creating another journal or changing selection order
    does not change the key for an otherwise identical case admission.
  - A journal lock excludes concurrent CLI owners. Mode-0600 journal snapshots
    are bounded to 4,390,912 bytes, strictly decoded and checked against their
    plan fingerprint and case identities. Initial creation is exclusive. Updates
    check the previous snapshot, stage and sync a private file, atomically replace
    it and sync its directory. No later case starts after a persistence failure.
    The CLI persists admission intent before POST, the accepted run ID before
    waiting, and a passing verdict before admitting the next case.
  - Explicit `--resume` requires real-action confirmation again and uses the
    original plan. Recorded run IDs are read, never replaced when missing.
    Uncertain admissions replay the original request and key only within 24 hours
    of the first attempt. This depends on retaining the original server record;
    the journal is not an independent execution receipt or an unlimited deduplication
    guarantee. Older uncertain admissions require inspection rather than replay.
  - Cases stop the suite at the first failed, unavailable, outdated or otherwise
    unresolved verdict. Pending runs are observed until the total invocation
    deadline. Resume revalidates earlier runs before admitting remaining cases.
    Before returning success, refresh every run and the app/version/deployment
    target. Observations remain timestamped snapshots, not an atomic multi-run
    server transaction or permission to publish another target.
  - Total invocation timeout defaults to 5 minutes and is capped at 1 hour.
    Requests retain the 10-second deadline; polling defaults to 3 seconds and is
    configurable from 1 second to 1 minute. No automatic execution retry,
    workflow continuation or cancellation is introduced.
  - Versioned JSON reports include outcome, exit code, target, per-case safe
    observations, summary counts and an explicitly confirmed resume command.
    Unstarted and unresolved cases stay visible. Reports omit idempotency keys,
    inputs, output values, credentials and raw backend errors. The private journal
    retains original keys and safe verification DTOs, not saved sample payloads.
    Once a journal is prepared, runtime failures produce the combined report;
    setup failures use ordinary CLI error diagnostics. Exit 0 requires every
    selected case to pass current assertions. Other assertion outcomes use 1,
    HTTP failures retain exits 2–6, overall timeout uses 124 and interruption 130.
  - Tests exercise CLI admission, checking, interrupted observation, missing
    records, target drift and persistence through local HTTP fixtures. They do
    not qualify deployed handlers or native guest execution. Dashboard suite
    presentation and release-policy integration remain separate work.
- **Rejected alternatives:** Implicitly selecting every saved case can execute
  unreviewed real actions. Parallel admission makes stop-on-first-failure less
  useful. Replacing run IDs after a failed read or generating fresh keys on resume
  can duplicate side effects. Storing payloads adds no recovery authority.
