# ADR-957: Sampled blocker incident lifecycle

## Status
Accepted.

## Context
Persistent blocker alerts need stable episode identities and explicit clearing versus loss-of-evidence signals for automation.

## Decision
Stall-enabled watch monitoring exposes active `blocker_incidents` and current-pass `blocker_transitions`. Use a random invocation namespace plus opening transition sequence for opaque episode IDs. Increment the sequence for every emitted transition. Preserve IDs while the confirmed episode persists; newly confirmed episodes receive new IDs. Never trust or restore saved incident aggregates. Clear only the event buffer at each new pass; repeated summary/callback/final snapshots retain identical event IDs/sequences for downstream deduplication.

Open an incident when the existing stall detector confirms an eligible blocker. Resolve only on a new, strictly later, fresh available/not-waiting observation, without a sampling gap, that reports a non-blocker waiting reason or terminal routing. Terminal routing can be enqueued, filtered or failed; resolution of a routing blocker says nothing about handler completion. Changes to another blocker, non-monotonic evidence and episode resets lose confirmation rather than resolve. Stale/future, unavailable, replaced, invalid, excessive-gap and missing-selection evidence also lose confirmation. Include guarded identities, original blocker reason, notification time and validated compact evidence where available.

CLI `--alerts-json` requires a stall-enabled watch, emits lifecycle JSON lines on stderr, and suppresses routine watch progress text. Final status remains stdout JSON; existing error reporting is preserved. Human output also uses lifecycle transitions. Deduplicate emitted alerts by sequence within this invocation and emit newly discovered final transitions. Stopping a watch does not close fresh active incidents automatically.

## Consequences
Incident state and current-pass events remain proportional to selected recipients, not watch history. IDs are stable within one invocation, not across resume; checkpoints are inspection snapshots, not a durable incident ledger. No network calls, backend scans, SQL, migrations, new quotas, recovery actions, stop conditions or compliance registries change.
