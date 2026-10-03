# ADR-452: Repair plans bound to saved route intent

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-438, ADR-446, ADR-448, ADR-451

## Context

Continuous monitoring exposes route policy violations, but repair planning
requires exporting current app intent into a local file. File-based plans bind
the supplied document, configuration and capture; they cannot establish that the
app's saved intent is still the revision the developer reviewed.

## Decision

Extend the existing route policy plan/apply API and CLI with saved source mode.
Require exactly one intent source, a captured deployment for saved planning, and
an optional expected saved revision for planning. Read saved requirements through
the same repeatable-read transaction as account, app, rules and capture. Memory
stores isolate saved values under their existing mutex.

Saved version 3 artifacts include requirements_revision in addition to the
existing normalized requirements_sha256. Include both in the plan fingerprint.
Omit the revision for file-based plans to preserve their existing fingerprints.
The CLI displays saved provenance and applies it without resubmitting an inline
copy. API/SDK apply callers must supply the reviewed revision in saved mode.

For new apply requests, read and compare saved intent after taking the existing
account/app/rule locks. Saved intent writers already take the app lock, so intent
cannot change before verification and commit. Preserve the saved snapshot during
persisted policy verification. Rebuild the entire plan using current saved intent;
all existing capture, configuration, account eligibility, bounded patch synthesis
and inventory checks remain in force. Only supported throttle/budget changes are
committed. A changed revision fails with HTTP 409 without policy writes/receipts,
even if a later revision restored the original normalized content.

Find a matching committed receipt before reading current intent or capture.
Identical retry requests return that historical outcome; changed requests with
the same key remain conflicts. Automatic rechecks own current recovery evidence.
There are no new schema columns, SQL queries, application probes or workers.

## Consequences

Customers move from monitoring alerts to reviewed repairs without synchronizing
local intent exports. Planning can pin the notification revision while using
current configuration and capture. Existing file-based planning supports proposed
intent independently of saved requirements. Saved source fields are optional for
legacy clients; Node/Python generation and the Go SDK expose the extended wire
contract. The Go value-based request surface uses explicit JSON marshaling to
omit absent inline intent on its supported Go baseline.
