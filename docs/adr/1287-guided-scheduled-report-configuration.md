# ADR-1287 · Guided scheduled-report configuration

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Add scheduled-report fields, separate edited/published schedule
  previews, and an observed setup checklist to the automation dashboard. Keep
  advanced JSON available and reuse the existing validation and publication API.
- **Why:** App owners should configure a common recurring report and understand
  its schedule without manually changing JSON or mistaking mocks for deployment
  readiness.
- **Consequences:**
  - An explicit editor-source choice selects report fields or JSON. Missing
    source retains JSON compatibility. Report fields patch only the name,
    schedule options and two handler paths; advanced settings are preserved.
    A different graph is rejected by the form and remains editable through JSON.
  - New guided reports have starts disabled. Saving, publishing and previewing
    do not enable them; activation remains an explicit versioned API action.
  - Edited previews first use API validation, then the shared pure schedule
    helper. They write neither state nor cursor and call no handlers. Published
    previews use the effective-definition/cursor API, with distinct labels and
    explicit unavailable feedback. Both show nominal times, not reserved starts.
  - The checklist uses observed automation API metadata. A successful eligibility
    check confirms the live default deployment. Earlier execution blockers leave
    deployment readiness unverified. Handler behavior and credentials are never
    inferred from a live deployment or passing mocks.
  - The existing browser session, MFA, named CSRF, verified-email, saved-version
    checks, publication receipts and execution admission gates remain in force.
    No public API, schema, scheduler policy or native admission changes are made.
- **Rejected alternatives:** Rebuilding a starter from form fields would discard
  advanced edits. Calling the published preview API to preview an unsaved draft
  would show the wrong definition. Probing handler routes while previewing could
  cause real actions and would exceed the meaning of a configuration preview.
