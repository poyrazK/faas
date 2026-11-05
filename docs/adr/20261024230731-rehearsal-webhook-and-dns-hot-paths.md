# ADR-20261024230731 · Rehearsal checks on webhook deliveries and private DNS stay off other apps' critical path

- **Status:** accepted
- **Date:** 2026-10-24
- **Amends:** [ADR-20261019120000](20261019120000-rehearsal-runtime-hot-path-isolation.md), [ADR-20261017201003](20261017201003-rehearsal-webhook-capture.md)
- **Decision:**
  - `scenario_webhook_delivery_fence` locks the delivery's `apps` rows only when a delivery is inserted. An update takes no lock, and it reads `scenario_test_action_policies` only when the row would become runnable (`pending`, `in_flight` or `failed`). Suppression is unchanged.
  - Private guest DNS gives each scenario DNS chaos policy read `ServiceDNSChaosLookupTimeout` (250 ms). After a failed read the node answers without chaos for `ServiceDNSChaosBackoff` (5 s) instead of reading again for every question. A name that cannot be a workload, or a non-IPv4 source, has no policy and is no longer reported as a store failure.
- **Why:** Both checks run for every app, rehearsing or not.
  - **Webhooks:** the fence locked `apps … FOR KEY SHARE` on every claim and completion. The dispatcher claims deliveries for many accounts in one statement, so a transaction holding one app `FOR UPDATE` stalled the whole batch. ADR-20261019120000 described these triggers as one primary-key lookup.
  - **DNS:** every `*.svc.gregale` question read the policy with the 2 s lookup budget. When the store was slow, each private answer for every guest waited up to 2 s and logged a warning.

## Why updates need no lock

A registration takes `apps … FOR UPDATE` on each member and refuses an app that already has webhook deliveries (`ScenarioActionAppUnstarted`). The insert lock orders every new delivery against that registration:
- A delivery inserted first makes the registration refuse the app.
- A delivery inserted after the registration sees the policy and is written `dead`.

An existing delivery therefore belongs to an app that has never been enrolled, or it was already suppressed when it was inserted. An update cannot race a registration. Policies are removed only with their app or account, so the policy read on an update still keeps a manual retry from making a suppressed delivery runnable.

## Consequences

- Webhook claims and completions no longer wait on `apps` row locks held by other work. Migration `20261024230705892` replaces only the function, which takes no table lock.
- During a store failure, DNS chaos for running rehearsals is skipped for up to 5 s. The read already failed open; chaos match evidence for that window comes out short, so such a rehearsal reports an incomplete or mismatched DNS fault count, never a pass it did not earn.
- Two related costs are recorded but not changed: each internal service call makes one uncached `ScenarioRehearsalNetworkRun` read, and each in-guest event publish makes one `ScenarioEventCapturePolicy` read. Capture-only enrollment is ungated, so neither read can be switched off by the containment gate.
