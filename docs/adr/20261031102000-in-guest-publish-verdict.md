# ADR: In-guest event publishes answer with vmmd's verdict

Date: 2026-10-31
Status: Accepted
Amends: [ADR 20261027125625](20261027125625-events-kill-switch.md) (events kill switch: in-app publishing), [ADR 20261026140000](20261026140000-event-app-sources-and-publish-rate.md) (publish rate)
Related: [ADR-022](022-vsock-resume-hook.md) (resume hook wire), [ADR 20261029093000](20261029093000-event-ga-gate.md) (event GA gate)

## Context

An app publishes an event from inside its VM by POSTing it to the in-guest
endpoint (`FAAS_EVENT_PUBLISH_ENDPOINT`). guest-init writes the event to vmmd
over vsock and closes the stream, and the endpoint then answers `202
{"accepted":true}`. vmmd validates and stores the event afterwards
(`cmd/vmmd/framework_ready_recv.go`, `dispatchEventPublish`) and can only
log a refusal. The blocker hunt of 2026-10-31 found that every refusal was
lost silently, though the app had already been told the event was accepted:

- an invalid envelope or a schema mismatch;
- the events kill switch (the docs promised `503`);
- the account's publish rate;
- a database error while storing it.

The fix is to answer only once vmmd has decided. Three constraints shape
how:

- **No half-close.** Firecracker's vsock does not reliably pass a guest's
  half-close through to the host. `pkg/fcvm/vmm.go` records it breaking the
  V6 metal test. A guest therefore cannot shut its write side and wait for a
  reply on the same stream.
- **Versions run side by side.** Release N runs beside N-1
  (`docs/release-compatibility.md`), and an older vmmd reads a publish
  stream until EOF.
- **Snapshots move between versions.** A VM restored from a snapshot keeps
  the guest-init and boot command line it was captured with, but it may be
  restored by a vmmd of either version.

## Decision

1. **New frame type.** A publish that waits for a verdict uses frame type
   `0x0B`: a 4-byte big-endian body length, then the body. vmmd answers with
   a 2-byte big-endian length and a JSON `api.EventPublishVerdict`
   (`status`, `code`, `retry_after_seconds`; at most 512 bytes). Neither
   side waits for EOF. vmmd runs the same publish path as type `0x07`, with
   a 3 s budget. guest-init waits at most 4 s for the answer.
2. **guest-init uses `0x0B` only when told the host answers.** vmmd adds
   `faas.eventpublish=verdict` to every app VM's cold boot. Every resume
   request carries `eventPublishVerdict: true`. A restored guest follows the
   vmmd restoring it: an older vmmd omits the field, so the guest falls
   back to the fire-and-forget `0x07` frame. An older guest-init never sends
   `0x0B`, and vmmd still handles its `0x07` frames.
3. **The endpoint returns the verdict.** It uses the codes of
   `POST /v1/events:publish`:

   | Status | Code | Cause |
   |---|---|---|
   | `202` | | Accepted |
   | `400` | `invalid_event` | Malformed envelope, or a reserved or foreign source |
   | `422` | `validation_failed` | Schema required, unknown or not matched |
   | `429` | `event_publish_rate_limited` | Over the plan's publish rate; `Retry-After` |
   | `503` | `events_paused` | Kill switch; `Retry-After: 30` |
   | `503` | `event_publish_unavailable` | Anything else |

   When the event reached vmmd but no verdict came back, the endpoint
   answers `503 event_publish_unconfirmed`. Publishing the same event again
   is safe, because the receipt keys on `(account, source, event id)`.

## Consequences

- An app learns of every refusal and can retry or fix the event. Nothing
  refused is acknowledged with `202` any more.
- A publish now waits for vmmd's database round-trip: one insert, typically
  milliseconds, bounded at 3 s.
- During the window where a VM booted under a new vmmd is served by an old
  one, which only happens when vmmd is rolled back under a running VM, the
  old vmmd waits for EOF that never comes. The guest gives up after 4 s and
  answers `503 event_publish_unconfirmed`, and the old vmmd drops the
  unknown frame. The event is refused explicitly, not lost silently, until
  the VM is restarted or restored.
- Apps whose code ignored the response are unaffected. Apps that treated
  `202` as stored now get the guarantee they assumed.
- No schema change. guest-init (runtime) and vmmd (node) release
  independently.
