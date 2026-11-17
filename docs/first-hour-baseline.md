# First-hour baseline — 2026-11-17

The deployed scripted CLI lane passed all seven acceptance steps against the
production API using the explicitly authorized existing Scale account. It used
one newly named private Node HTTP test app, submitted four releases, and verified
that the test app no longer resolved after cleanup. Existing apps were not test
targets. No global CLI installation or platform configuration was changed.

## Observations

| Measurement | Observed |
|---|---|
| First verified private response | 201.287 s — 3 min 21 s |
| Second verified deployment, from the same starting point | 379.953 s — 6 min 20 s |
| Interrupted waiter to verified edited response | 173.501 s — 2 min 54 s |
| Injected startup failure to verified repair | 368.889 s — 6 min 9 s |
| All customer milestones through verified repair | 748.846 s — 12 min 29 s |
| Checked cleanup | 1 s, passed |

The first release returned the expected application version through the
owner-authorized check while a credential-free public request was denied. The
edited release retained the same app identity. After interrupting its waiter,
continuation followed the accepted UUID and history contained exactly two
releases. The injected startup cause appeared in the failed release's logs;
repair produced a new verified release and history contained exactly four.

## Release-readiness finding

The installed `v0.1.18-rc.347` and the then-latest published `v0.1.18-rc.348`
both lacked `release verify`. The latter was checked from a temporary release
archive verified against `CLI-SHA256SUMS`. The installed-CLI pack correctly
stopped before deploying anything and reported **blocked**.

The successful deployed run therefore used a temporary candidate CLI built from
backend main at `676100529dcc13b8d4296504d57cc4aa0faefedb`, with no CLI product
source changes. The candidate version, binary SHA-256 and uncommitted acceptance
worktree marker are retained in the evidence. This is candidate qualification,
not proof that users receive the command from the published installer.

The next release needs to carry the merged command, followed by the same
acceptance run against the released artifact.

## Scope and remaining evidence

This is one scripted observation after authentication, with immediate rollouts
and a Node fixture on a Scale account. It is not a latency percentile, a
reliability estimate, a Free-plan qualification, or a measurement of human
onboarding time. Cleanup is required and timed separately from the customer
milestone clock.

Fresh installation/login, interactive `gregale start`, Python/Go projects and
real coding-agent runs remain **not measured**. The pack supplies the human
checklist and private-deploy/edit agent tasks; no synthetic or unit-test result
is presented as their live baseline. Agent results can be imported only when
their target and CLI checksum match the journey.

## Evidence and reproduction

- [Candidate scorecard](ops/evidence/20261117-first-hour/candidate-baseline.md)
  and [machine-readable results](ops/evidence/20261117-first-hour/candidate-baseline.json).
- [Installed-CLI scorecard](ops/evidence/20261117-first-hour/installed-baseline.md)
  and [release-readiness observation](ops/evidence/20261117-first-hour/release-readiness.json).
- [Acceptance guide and run commands](first-hour-acceptance.md).

Only sanitized aggregates and provenance are checked in. Raw command output,
account identity, application response previews and local paths remain outside
the report artifacts.
