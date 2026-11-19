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

These authenticated Scale journeys exclude fresh login and interactive Start.
The separate Free Node observation below covers those steps on its recorded
platform. These individual observations do not establish a latency trend or
reliability estimate.

## Fresh-onboarding installer preflight — 2026-11-17

The new Free Node onboarding lane has an installation-only observation on
**darwin/arm64**. The published installer selected `v0.1.18-rc.349` by default
and installed it into an isolated directory. The installed binary matched the
independently checked release archive, version and tagged source. Installation
and provenance checks took **27.886 s**; removing the isolated connection took
**0.438 s**. The connection was confirmed signed out. The original CLI and
login were preserved, and no account or app was created by this preflight.

Its overall result is **blocked**, with reason
`installer_only_browser_login_not_attempted`. That report does not qualify a
Free plan, browser login, Start or edit. It records the harness's base revision
and dirty-worktree marker; the complete lane uses separate browser authorization.

## Fresh Free Node onboarding — 2026-11-17

The published installer/default channel selected `v0.1.18-rc.349` on
**darwin/arm64**, and the full browser-assisted Free Node lane passed **8/8**
phases against the production API. The installed bytes matched the published
archive and tagged source. The run began signed out, completed normal browser
authorization, and confirmed the expected separate account was Free with no
apps. It used the committed harness at
`143bfe14d73a11c89a770cf14d4084a99cfc4206`, clean when provenance was captured.

Start created the Node HTTP starter with its normal deployment defaults,
verified its initial greeting, then applied the walkthrough's greeting edit.
Both HTTP 200 responses were bound to their exact app/release/replica; the
edited response proved a distinct deployment and changed source of the same
app. Exactly one app and two releases existed. The app was deleted and
confirmed absent, the account returned to zero apps, and the isolated CLI login
was revoked and removed. The original CLI configuration and login were isolated
from the run. A read-only check afterward confirmed the original CLI remained
signed in to the separate Scale account.

| Measurement | Free Node observation |
|---|---|
| Published installation and provenance | 32.185 s |
| Fresh CLI browser authorization | 130.088 s |
| First verified greeting, from before installation | 297.377 s — 4 min 57 s |
| Greeting edit to verified response | 129.767 s — 2 min 10 s |
| Installation through verified edit | 427.159 s — 7 min 7 s |
| Checked app cleanup | 3.526 s, passed |
| Checked CLI logout and connection removal | 1.289 s, passed |

The clock includes observer/browser interaction, qualification checks and
scripted terminal input. It excludes account signup, email verification and
initial browser-session preparation. This records controlled acceptance with
normal Start rollout defaults, not unassisted human onboarding time. Other
platforms, runtimes and agent performance are not inferred from it.

## Earlier Free onboarding attempts

Three unsuccessful attempts are retained separately. None created an app:

| Attempt | Observation |
|---|---|
| Unverified email | Installation passed, but CLI authorization refused the new account until its email was verified. The login expired, and the isolated connection was removed. The email link was subsequently confirmed through the normal browser flow in the browser signed in to that account. |
| Installer write failure | The installer could not write its release download locally and stopped before login. Only this task's disposable installed binaries were removed before retrying; the shared build cache was preserved. |
| Absence-check contract | Browser login passed and the API confirmed Free with zero apps. The harness incorrectly expected exit 1 for a missing app; rc.349 correctly returned exit 4 and `not_found`. This stopped before Start, and the isolated login was revoked and removed. Both absence checks were corrected and a process-level regression added. |

Their reports remain failed. Subsequent runs do not replace them or turn these
observations into a reliability estimate. Account creation, email verification
and browser preparation precede the measured installation/login lane.

## Scope and remaining evidence

The seven-step Scale observations begin after authentication, use immediate
rollouts and a Node fixture, and retain their own scope. The separate Free
Node lane measures a fresh published installation, normal browser login,
interactive Start and its edit walkthrough with normal rollout defaults on
the recorded platform. Cleanup is required and timed separately from the
customer milestone clock. None establishes a latency percentile, reliability
estimate or unassisted human onboarding time.

Python/Go projects, other platform archives, shell integration, real coding
agents and interruption/restart of interactive Start remain **not measured**.
No synthetic or unit-test result is presented as their live baseline. Agent
results can be imported only when their target and CLI checksum match the
journey; the older scripted CLI scorecard does not inherit the separate
onboarding lane's evidence.

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
- [Passed Free Node onboarding](ops/evidence/20261117-first-hour/free-node-rc349-darwin-arm64-onboarding.md)
  and [machine-readable results](ops/evidence/20261117-first-hour/free-node-rc349-darwin-arm64-onboarding.json).
- [Unverified-email attempt](ops/evidence/20261117-first-hour/free-node-unverified-login-failure.md)
  and [machine-readable attempt](ops/evidence/20261117-first-hour/free-node-unverified-login-failure.json).
- [Installer-write failure](ops/evidence/20261117-first-hour/free-node-installer-write-failure.md)
  and [machine-readable attempt](ops/evidence/20261117-first-hour/free-node-installer-write-failure.json).
- [Absence-contract failure](ops/evidence/20261117-first-hour/free-node-absence-contract-failure.md)
  and [machine-readable attempt](ops/evidence/20261117-first-hour/free-node-absence-contract-failure.json).
- [Acceptance guide and run commands](first-hour-acceptance.md).

Only sanitized aggregates and provenance are checked in. Raw command output,
account identity, application response previews and local paths remain outside
the report artifacts.
