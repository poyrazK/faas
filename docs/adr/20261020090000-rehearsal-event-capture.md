# ADR-20261020090000 · Rehearsal event capture

- **Status:** accepted
- **Date:** 2026-10-20
- **Amends:** [ADR-20261017201003](20261017201003-rehearsal-webhook-capture.md), [ADR-20261017201004](20261017201004-sealed-rehearsal-action-comparison.md), [ADR-20261017201005](20261017201005-rehearsal-network-containment.md)
- **Decision:** Events that an enrolled app publishes are captured into its run's evidence instead of entering the account's event bus. The sealed action snapshot carries those events and per-type counts of the ordinary webhook events the platform suppressed, and rehearsal comparisons compare both.
- **Why:**
  - A contained guest's published event was silently dropped in vmmd. The app believed it had published, and the rehearsal could not see the difference between one publish and two.
  - A capture-only rehearsal, which keeps its ordinary network, published real events that fanned out to the account's production apps.
  - Suppressed ordinary webhook deliveries were counted but never compared, so a candidate that emitted extra lifecycle webhooks still passed.

## Decision

### Capture

- **Contained instances.** vmmd routes an in-guest publish from a contained instance to a capture callback with the instance's installed identity: app, account and run. It never reaches the publisher.
- **Every other instance.** The ordinary publish path first asks the store whether the app has a rehearsal policy; a capture-only enrolled app is captured as well.
- **Normalization.** Captured events go through exactly the same envelope normalization, reserved-source check and schema validation as a production publish. An event production would reject is rejected here too.
- **Storage.** `scenario_captured_events` holds one row per publish: source, type, schema version and data. Platform-generated IDs and times are not stored. Rows cascade with the policy.
- **Admission.** The capture takes the run's advisory lock, the same lock the seal takes. An event is therefore either in the sealed snapshot or refused as late (`ErrScenarioActionsSealed`); it can never commit unseen.
- **Fail-closed.** A contained instance with no matching policy or run is refused and never published. When the policy lookup fails, the event is not published.
- **Bounds.**
  - 256 events per run; one extra row marks overflow.
  - 64 KiB of data per event. Larger data is kept only as an oversized marker.
  - Overflow or oversized events make the snapshot `incomplete`; they never become a passing empty inventory.

### Snapshot

The fields are additive and apply to version 1 and version 2 snapshots alike:

| Field | Content |
|---|---|
| `event_capture_version` | `1` from servers with this decision |
| `published_events` | ID, workload, source, type, schema version and data |
| `suppressed_webhook_events` | Per workload and event type, the count of ordinary deliveries suppressed by the capture fence. `operation.effect` is excluded; managed effects remain captured intent. |
| `incomplete_captured_events` | Oversized or overflowing inventory |

### Comparison

- **Published events** compare as a multiset per workload, source and type, using schema version and canonical data. Duplicates count, and event IDs and times never participate.
- **Suppressed webhook events** compare by count per workload and event type. Their payloads carry platform-generated deployment and instance identifiers that differ between any two runs.
- **Mixed versions.** A comparison across different `event_capture_version` values is inconclusive. A snapshot with event evidence but no version is invalid.

## Consequences

- Capture-only rehearsals no longer publish to the account. This is the same exclusion ADR-20261019180000 applies to route-discovery events.
- Every in-guest publish of a non-enrolled app costs one primary-key lookup on `scenario_test_action_policies` before it is appended. Wake and resume paths are unaffected.
- A publish that fails before the store, such as invalid JSON or a schema violation, is not captured, as in production. The guest receives no error on this datagram path in either case.
- Older CLIs ignore the new fields and compare as before. Newer CLIs against older servers compare with `event_capture_version` 0 on both sides, which is in scope and not inconclusive.

## Rejected alternatives

- **Folding events into the webhook-shaped `intents` list.** Every intent requires a receiver and operation identity, so events would need placeholder identities that validation would have to special-case.
- **Comparing ordinary webhook payloads.** Generated identifiers would make every rehearsal fail.
- **A database trigger on `events` that redirects enrolled apps.** Published rows carry the account, not the producing app.
