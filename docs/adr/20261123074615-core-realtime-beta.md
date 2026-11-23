# ADR-20261123074615: Core realtime joins beta; retained history stays preview

- **Status:** accepted (owner direction, 2026-11-23: "start step 1" after the realtime beta-readiness review)
- **Date:** 2026-11-23
- **Amends:** ADR-20261019102657 (realtime preview rollout), the tier recommendation in `docs/ga-capability-inventory.md` (row 112)
- **Related:** ADR-156 (managed realtime), ADR-1323 (fair use, outbound budget, usage), ADR-1324 (occupancy), ADR 20261021080640 (per-feature flags), ADR-20261023115741 (capability flags)

## Context

Realtime was one preview row (112) covering everything from endpoints to
retained history. The production realtime journey (rc.373, 2026-11-23) passes
every step that does not need the resumable protocol: endpoint create, v1
WebSocket connect, unsigned handshake refused, connection listed, server-side
subscribe, live publish, direct send and close. Fair use, the outbound budget
and usage recording are on in the apid and realtimed units. The resumable
protocol and everything retained have never run on production: their daemon
switches default to `0`, and retained history has no plan limits yet
(`FAAS_REALTIME_RETAINED_PREVIEW_ENABLED`: "off pending plan entitlements and
fleet qualification").

## Decision

1. **Beta:** the `realtime` and `realtime-connections` capabilities. That is
   endpoints, WebSocket and SSE connections held while the app is parked,
   authentication and callbacks, the original protocol, live publish, direct
   and principal send (live delivery, optional receipts), connection
   operations and fair-use limits. Beta is on for every paid plan by the tier
   default (`platformflags.TierDefault`).
2. **Preview, unchanged:** `realtime-channels`, `realtime-occupancy`,
   `realtime-tickets`, `realtime-access`, `realtime-workers`, `realtime-push`
   and `realtime-publishers`, plus everything ADR-20261019102657 puts behind
   `realtime_preview` (retained delivery, the resumable protocol, presence,
   signals, endpoint slugs and channel rules, `kind: realtime` triggers, the
   `realtime:` section of `gregale.yaml`). New inventory row 176.
3. **Route ownership follows the split.** The capability gate picks the
   longest matching catalog route, and the core publish route
   (`…/channels/{channel}/publish`) sat under the preview `realtime-channels`
   prefix, principal send and its receipt under `realtime-access`. The
   catalog gives those three to `realtime`, so an account without the preview
   can publish and send.
4. **Public spec and SDK.** Principal send is now in the GA and beta spec;
   its retained-only request fields, the `retained` delivery value and the
   retained-only response fields join `realtimepreview.Fields` and
   `realtimepreview.FieldValues`, so the served spec and `sdk/openapi.yaml`
   leave them out until every account sees the preview.
5. **Docs.** `docs/realtime.md` is published, with the beta and preview split
   at the top and a preview note under every preview section (headings, and
   so their anchors, are unchanged).

## Evidence required before customers rely on it

- The realtime journey (`tests/journeys/realtime_test.go`), green on
  production for the beta steps.
- A load run against production: many idle connections and channel fan-out
  across the compute nodes, with its evidence under `docs/ops/evidence/`.

## Consequences

- Retained history reaches beta only with plan limits for history storage
  and retention and a fleet qualification run, in a later ADR.
- After release, check production for a stored operator value on the
  `realtime` and `realtime_connections` flags: a stored value overrides the
  tier default.
