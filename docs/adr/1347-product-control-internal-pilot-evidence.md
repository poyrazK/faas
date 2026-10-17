# ADR-1347 · Product Control internal pilot evidence

- **Status:** accepted for internal implementation; deployed pilot pending
- **Date:** 2026-10-17
- **Decision:** Extend the existing read-only pilot verifier with optional scoped
  Customer Help comparisons and operator-recorded journey evidence. Preserve
  default-off service flags and separate local evidence from live qualification.

## Help evidence

Version-one pilot manifests remain valid. Optional `help_cases` reference only
the three baseline tenants. Run requests must reference a selected operation of
that same tenant; feature requests must name the pilot feature. Request IDs and
names are unique. Every case records an expected kind, status, version and
reviewed hashes of the initial message, latest response and original context.

The verifier uses only owner-scoped GET requests. It checks returned app, tenant
and request identities before retaining evidence. Run context must also match
the selected admission's original feature, revision, plan and month. It compares immutable context
and initial text hashes with current status/version/response expectations.
Changed observations block the help comparison without relabeling valid ledger
evidence as invalid. Reports include references, timestamps and hashes, never
help messages, responses or diagnostic JSON. Existing files cannot be overwritten.

Text hashes use exact UTF-8 bytes, including line endings. Context hashes use
the existing canonical JSON encoder over `api.ProductHelpContext`. These hashes
are private evidence, not an anonymization guarantee or authorization token.
Owner reads cannot prove customer visibility, current membership, permission
revocation, browser recovery or delivery. Those remain explicit live scenarios.
`gregale product pilot help` inspects scoped hashes without text and marks
its result `inspection_only`; reviewed expectations remain a separate step.

## Journey records and friction

Optional `journeys` link a baseline customer to a selected run and help case.
Each records the observer's sign-in start time and chronological observations
of `sign_in`, `paid_feature`, `usage`, `plan_change`, `help_request` and
`owner_response`. An observation is `observed` or `blocked` and references a
local evidence file and its reviewed SHA-256. Recovered blocked attempts remain
counted. Missing customers/stages or absent success/response evidence leave the
three-customer journey incomplete.

The report measures first observed successful work from the operator-recorded
sign-in start, counts blocked observations, and reports elapsed time from help
creation to its latest resolved update when that scoped comparison matched.
The last measure includes time across any reopenings; the service has no edit
journal and cannot infer the first reply or first resolution. Operator timing
is an observation, not a product latency benchmark or independently verified
user behavior. A report always retains `qualification_state: unverified`.

At most three journeys, thirty-two observations each, and four MiB per nonempty
regular evidence file are accepted. Paths must be local to the manifest directory;
root-relative opens reject symlink escapes. Hashes and byte counts are retained
without copying contents. Observations after the report time are rejected.
Limits remain centralized in `pkg/api/limits.go`; the manifest stays at 64 KiB.
Records must be reduced to exclude credentials, raw HAR/cookies, invitation
tokens and customer-authored help text.

`help_state` is `not_requested`, `observed` or `blocked`; `journey_state` is
`not_requested`, `recorded`, `incomplete` or `blocked`. Exit one reports a ledger,
help or artifact mismatch; exit three reports an incomplete requested journey
unless a mismatch already requires exit one. Legacy ledger-only success remains
exit zero. No exit code or manually completed record grants native qualification.

## Migration check and release boundary

The older migration-scope assertion assumed every allowlisted issued SQL file
needed byte rewriting. The runtime-default-base migration already uses
unqualified declarations and correctly follows the migration search path.
Its original bytes must remain unchanged. The static check now distinguishes
that case; PostgreSQL integration also asserts that its actual function belongs
to the isolated schema and never escapes to public. No issued SQL is edited.

The release candidate is prepared locally. Running the deployed pilot requires
a selected internal API, owner access, real test identities and an independently
qualified native execution lane. Preparation never enables flags, deploys code,
sends invitations, retries work, repairs outcomes or changes billing/permissions.
Customer Help and all other service surfaces keep the existing default-off gate.
