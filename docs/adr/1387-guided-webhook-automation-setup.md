# ADR-1387 · Guided webhook automation setup

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Extend the automation dashboard with event-starter fields, scoped
  inbound endpoint selection, confirmed versioned binding/removal and explicit
  provider-event receipt inspection. Reuse the existing authoring and webhook APIs.
- **Why:** Verified webhook execution already exists, but app owners must leave
  the authoring dashboard to connect a starter to ingress and find its run.
- **Consequences:**
  - An explicit editor source selects webhook fields or JSON. Guided fields
    patch the event source/type/filter and lookup/record paths, preserving
    advanced settings. Different graphs remain editable through JSON. New guided
    event definitions have starts disabled until explicit activation.
  - Saved-definition comparison tolerates JSON object-key reordering by storage,
    while preserving array order, exact numeric literals and revision checks.
    This keeps nested filters/input mappings usable without accepting changed values.
  - Platform event trigger settings are distinct from verified endpoint binding
    patterns/filters. Verified ingress retains its reserved source and signed
    provider envelope; bindings can target any compatible published automation.
  - Endpoint selection proves the app/account identity before further reads or
    writes. Browser projections omit routing tokens, signing-secret metadata and
    event bodies. Receipt lookup requires the selected automation identity; run
    links additionally verify the actual run's app and workflow.
  - Binding takeover requires explicit consent, the observed binding version and
    a UUID idempotency key. A repeat reaches the existing idempotency wrapper even
    after the binding version advances. Removal requires separate confirmation
    and the observed version, and is offered only for this automation's binding.
    Failure disables routing controls until a fresh GET and confirmation.
  - Binding changes affect future acceptance; accepted events retain their
    captured routing. Removal restores ordinary delivery rather than pausing
    ingress. Binding an enabled endpoint to an enabled automation can start real
    actions immediately. Endpoint enabled state is displayed, never changed here.
  - Receipt reads show accepted/ignored/routing state and verified run links.
    Existing APIs do not retain retry counts, so the UI explains receipt reuse
    without claiming that a false duplicate flag proves no retries occurred.
  - Session/MFA, named CSRF, verified-email and write-scope checks remain in force.
    No schema, public API, signature-verification or native admission changes.
- **Rejected alternatives:** Treating draft trigger filters as the binding's
  live settings would conflate independent routes. Sending a test event during
  preview could perform real actions. Silently replacing ordinary delivery or
  inferring handler readiness from mocks would hide consequential behavior.
