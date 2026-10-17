# ADR-1285 · Guided automation dashboard

- **Status:** accepted
- **Date:** 2026-10-14
- **Decision:** Add an app-owner browser workspace for starter selection, JSON
  authoring, validation, simulation, saved-draft checks, publication, explicit
  manual runs, and metadata inspection. Reuse the existing automation and
  workflow API handlers through an in-process browser adapter.
- **Why:** The existing CLI and API expose these capabilities, but customers
  need one guided route from a starter to an inspected first run.
- **Consequences:**
  - `GET /dashboard/apps/{slug}/automations` reads intent and retained run
    metadata. Starter selection and previews never save or execute workflows.
  - POST actions require the existing session/MFA boundary and an account/app
    bound named CSRF envelope. API read/write scopes and verified-email checks
    remain enforced. Versions are submitted explicitly; a failed or stale form
    never silently adopts a newer revision.
  - Checking and publication require the editor definition to match the saved
    revision. Server-issued publishing receipts and current app policy govern
    publication; the browser cannot weaken coverage requirements or manufacture
    check evidence. Manifest ownership transfer remains explicit.
  - Authoring samples stay in the transient browser form and are preserved across
    save/check/publish responses. The browser never stores them in a new table.
    Real run inputs, outputs and raw errors are excluded from inspection views.
  - A manual run requires separate confirmation of real side effects. Only its
    input is submitted; mocks remain simulation-only. A stable per-form key
    reuses existing workflow admission deduplication on repeated submission.
  - Shared embedded starters serve both the CLI and browser, including every
    existing check scenario. Runtime flags, external integration authorization,
    tenant requirements, admission budgets and native qualification remain
    separate execution gates. This change does not activate native admission.
- **Rejected alternatives:** Direct state mutations from the dashboard would
  duplicate API policy and audit behavior. Browser-supplied check summaries
  would bypass server verification. Persisting sample data alongside definitions
  would introduce a new privacy and retention contract.
