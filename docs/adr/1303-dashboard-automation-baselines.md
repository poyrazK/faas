# ADR-1303: Manage named automation baselines in the dashboard

## Status

Accepted

## Context

ADR-1302 provides attributed, revision-protected named baselines through the API
and CLI. Dashboard users need to inspect trust decisions and qualify retained
suites without performing new workflow actions.

## Decision

Add a named-baseline panel outside the automation editor. A GET selector reads a
known name (default `production`), its head and bounded promotion history. It
does not discover names or execute workflows. Display promotion policy, target,
actor, timestamp and immutable receipt identity. Historical receipts describe
promotion-time decisions, not present evidence certification.

Use a CSRF-protected preview POST so policy JSON never enters query strings.
Read the selected suite's coherent self-comparison using the existing comparison
API and evaluate it with the same pure promotion evaluator as the state store.
Show failed and inconclusive reasons, missing required cases and original-run
age. Preview does not check assertions, run cases or persist metadata.

A separate confirmation POST sends the explicit suite/target, canonical version
2 policy, original expected head revision and a stable promotion UUID through the
existing authenticated promotion API handler. The backend remains authoritative:
it rechecks evidence and serializes concurrent writes. Confirm/retry preserves
exact intent, including the UUID and original revision, across an uncertain
response. A replay may return a historical receipt; refresh the head separately
and describe the returned receipt as a recorded promotion, not necessarily the
current head. The adapter derives actor identity from the cookie session and
strips competing bearer credentials.

Render a shell-quoted CI command using the selected retained candidate, named
baseline and explicit target. Named gates inherit the promoted policy. A
candidate must differ from the baseline suite.

## Consequences

Dashboard reads and preview work with execution disabled. GET/refresh never
promotes baselines or enables suite execution. Draft contents and private run
payloads stay out of these forms and URLs. Existing retention, quotas, API
scopes, verified-email requirement and session/CSRF protections remain in force.
No new database schema, public API or SDK is necessary.
