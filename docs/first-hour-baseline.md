# First-hour baseline — 2026-11-17

The initial candidate CLI run passed all seven acceptance steps against the
production API using the explicitly authorized existing Scale account. It used
one newly named private Node HTTP test app, submitted four releases, and verified
that the test app no longer resolved after cleanup. Existing apps were not test
targets. No global CLI installation or platform configuration was changed.

## Initial candidate observations

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

The released-artifact run below subsequently qualified the command in
`v0.1.18-rc.349` for the recorded platform.

## Released-artifact qualification — 2026-11-17

The published `v0.1.18-rc.349` **darwin/arm64** archive passed artifact checks
and all **7/7** deployed journey steps on the explicitly authorized Scale
account. Its checksum matched `CLI-SHA256SUMS`; the binary reported the
selected version and tagged source commit
`37b55bddef0ce36d22b524c9270b290ecd529e81`, and included `release verify`.
The tool used a temporary copy of the published binary, without rebuilding it
or replacing the installed CLI. The app was deleted and verified absent.

| Measurement | Released-artifact observation |
|---|---|
| First verified private response | 138.560 s — 2 min 19 s |
| Second verified deployment, from the same starting point | 269.123 s — 4 min 29 s |
| Interrupted waiter to verified edited response | 125.488 s — 2 min 5 s |
| Injected startup failure to verified repair | 340.899 s — 5 min 41 s |
| All customer milestones through verified repair | 610.025 s — 10 min 10 s |
| Checked cleanup | 1.99 s, passed |

This is one completed released-artifact journey. An earlier attempt passed
artifact checks but the local Go harness failed to build because shared-cache
artifacts were unavailable, before any app was created. That attempt remains
recorded as failed in the evidence; the successful rerun does not replace it.
The runner now preserves existing per-package Go compilation guards when
adding its development defaults. Downloading and compiling the test harness
are outside the customer milestone clock.

The separate release-qualification workflow uses the same pack. Its configured
acceptance environment uses a separately named `apps:read,deploy:write` key,
with the environment restricted to `main`. The scoped key was checked against
the same account and successfully verified an exact private test release with
HTTP 200. This setup check is not a completed GitHub-hosted journey; those runs
will retain their own reports after the workflow lands.

Fresh login, interactive Start and other platform archives remain unmeasured.
The installer-only observation below measures default channel selection and
installation separately. The two completed scripted
observations do not establish a latency trend or reliability estimate.

## Fresh-onboarding installer preflight — 2026-11-17

The new Free Node onboarding lane has an installation-only observation on
**darwin/arm64**. The published installer selected `v0.1.18-rc.349` by default
and installed it into an isolated directory. The installed binary matched the
independently checked release archive, version and tagged source. Installation
and provenance checks took **27.886 s**; removing the isolated connection took
**0.438 s**. The connection was confirmed signed out. The original CLI and
login were preserved, and no account or app was created by this preflight.

Its overall result is **blocked**, with reason
`installer_only_browser_login_not_attempted`. No Free plan, browser login,
Start or edit result has been qualified. The report records the harness's base
revision and dirty-worktree marker. The complete lane requires normal browser
authorization to the selected separate Free account before it can run.

## Scope and remaining evidence

These are individual scripted observations after authentication, with
immediate rollouts and a Node fixture on a Scale account. They are not latency
percentiles, a reliability estimate, a Free-plan qualification, or measurements
of human onboarding time. Cleanup is required and timed separately from the
customer milestone clock.

The published install has the separate preflight above. Fresh browser login,
interactive `gregale start`, Python/Go projects and real coding-agent runs
remain **not measured**. The pack supplies the browser-assisted Free Node lane
and private-deploy/edit agent tasks; no synthetic or unit-test result is
presented as their live baseline. Agent results can be imported only when
their target and CLI checksum match the journey.

## Evidence and reproduction

- [Candidate scorecard](ops/evidence/20261117-first-hour/candidate-baseline.md)
  and [machine-readable results](ops/evidence/20261117-first-hour/candidate-baseline.json).
- [Installed-CLI scorecard](ops/evidence/20261117-first-hour/installed-baseline.md)
  and [release-readiness observation](ops/evidence/20261117-first-hour/release-readiness.json).
- [Released-CLI qualification](ops/evidence/20261117-first-hour/released-rc349-darwin-arm64-qualification.md)
  and [machine-readable qualification](ops/evidence/20261117-first-hour/released-rc349-darwin-arm64-qualification.json).
- [Released-CLI scorecard](ops/evidence/20261117-first-hour/released-rc349-darwin-arm64-baseline.md)
  and [machine-readable scorecard](ops/evidence/20261117-first-hour/released-rc349-darwin-arm64-baseline.json).
- [Earlier failed harness preflight](ops/evidence/20261117-first-hour/released-rc349-preflight-failure.json).
- [Fresh-onboarding installer-only preflight](ops/evidence/20261117-first-hour/free-node-installer-preflight.md)
  and [machine-readable preflight](ops/evidence/20261117-first-hour/free-node-installer-preflight.json).
- [Acceptance guide and run commands](first-hour-acceptance.md).

Only sanitized aggregates and provenance are checked in. Raw command output,
account identity, application response previews and local paths remain outside
the report artifacts.
