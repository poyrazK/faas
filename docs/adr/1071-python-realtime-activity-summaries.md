# ADR 1071: Python realtime activity summaries

Status: Accepted; runtime validation deferred.

## Context

The Python presence directory supplies combined online/activity snapshots. Applications still repeat filtering, current-user exclusion, labeling and typing-text formatting already available in Node.

## Decision

Export RealtimeActivitySummary, RealtimeLabeledActivityParticipant, summarize_realtime_activity and format_realtime_typing_summary from the existing handwritten presence module and generator barrel. Project a presence summary into member-ID-sorted viewers and active members, with activity names sorted. Optional names filter activities while retaining viewers; exclude_member_id removes the member from both sets/counts. Omit expired activity at projection time. Expose included identity and session counts separately.

Default labels to verified member IDs or call an application label function with isolated member data. Require strings of at most 512 characters. Keep labels as untrusted presentation data. Copy returned state/activity data; never mutate inputs. Bound summary members/activities to 8192 and name filters to 64; reject duplicate members or duplicate named activity and mismatched channel/member identities.

Provide an English convenience typing formatter with max_labels 1–10. Recheck expiry, show overflow counts and handle singular/plural; return empty text when nobody is typing. Use active participants directly for localization/other activity types. Do not add timers or transport. Label failures propagate from the projection; directory callback isolation remains unchanged.

## Consequences

Python consumers can render user-facing typing/editing indicators consistently. Projection is process-local presentation, not durable state or an identity verification mechanism. Escape labels for their UI context. No routes, grants, schema or migration changes. Static lint/compilation, SDK AST/export checks and diff checks verify the implementation; runtime validation remains deferred by user request.
