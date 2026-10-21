# ADR-20261019120023: Health-gated HTTP environment promotion

## Status

Implemented behind `FAAS_PROMOTION_HEALTH_ENABLED=1`. The default remains off
until dedicated native KVM qualification passes. No production admission or
native guest execution was performed in this workspace.

## Context

Promotion's record verifier proves artifact, graph and configuration identities.
It cannot prove that the destination guest boots or that the promoted code can
use destination dependencies. Source qualification cannot establish that either:
destination secrets, credentials and bindings remain destination-owned.

## Decision

`projects environments promote --sync-config --verify` adds a durable HTTP health
gate to ADR-20261019120022 snapshot-aware promotion. Both environments need complete active
HTTP graphs, pinned release configuration and at least 21 minutes of revision
retention. Each HTTP workload needs a configured read-only GET/HEAD route-health
probe; the project supports at most five distinct probes. Workers, migrations,
sidecars and generated source service URL variables remain outside this milestone.
Existing promotion behavior remains available without `--verify`.

Preview and protected-environment approval bind the exact probe contract into
the promotion hash. Admission recomputes it, then saves it with the operation.
Later route-policy edits do not rewrite an admitted operation's contract. The
execute endpoint returns 202 with `status=running`; it does not stage candidates
or report successful promotion. The existing CLI `--wait` polls durable status.

A leased APID worker prepares zero-traffic candidates through the existing
promotion path. It uses the atomic deploy critical probe machinery, with private
challenges bound to exact app/deployment IDs and the destination environment.
The gateway handles readiness and normal authentication, including cold wake.
Challenges never bypass customer auth and never appear in status or audit data.
Evidence contains bounded counts, never request/response bodies or credentials.

Preflight requires two complete healthy rounds at least one minute apart. Every
configured endpoint must return an attributed 2xx; the destination's previous
release is sampled alongside the candidate. Unknown, incomplete, unauthenticated,
redirected or unexpected-status results reset consecutive counts. A failing
baseline also prevents qualification. Two candidate regressions with a healthy
baseline fail before cutover and preserve the active release. Unavailable probes
expire the ten-minute preflight deadline without publishing candidates.

Publication checks health under the existing project/promotion locks. It requires
an unexpired cutover checkpoint and fresh healthy baseline evidence, in addition
to existing artifact/configuration/qualification guards. The same transaction
publishes graph/configuration and starts a separate ten-minute post-cutover
verification window, clearing the preflight sample counters. A crash between
publication and a response therefore resumes destination verification.

Success requires two new healthy rounds, record verification and an atomic final
configuration/graph/workload identity check. Repeated candidate regressions with a
healthy baseline enter durable rollback, using existing atomic graph/configuration
restoration and retained variable pins. An expired post-cutover window or failed
record verification only authorizes restoration with fresh healthy baseline
evidence. Otherwise status requires operator action. New destination intent stops
the operation; recovery never overwrites that intent.

The health journal holds phase, generation, policy, deadlines and bounded sample
evidence. Generation CAS and minimum intervals prevent duplicate rounds. Leases
serialize external preparation/probe actions, expire after worker loss and are
released only by their owner. Existing admitted operations continue reconciling
if the admission flag is later disabled. Rollback checkpoints survive restart.
The journal is operational state and follows the promotion's lifetime.

## Consequences and qualification

`verified` now means the configured destination paths passed for this opt-in
mode. It does not prove arbitrary application behavior or schema compatibility.
Applications must provide read-only probes that exercise meaningful dependency
paths. Authentication failures remain unknown health; probes do not receive
special credentials or an authentication bypass.

Memory/PostgreSQL tests exercise health decisions, stale observations, cutover
admission, worker leases and destination edits. API tests exercise admission,
policy identity, preflight, restart, post-cutover verification and guarded rollback.
Before enabling the flag on production, complete the dedicated-host procedure in
[promotion health qualification](../ops/promotion-health-native-acceptance.md),
including real guest/dependency failures, daemon restart and final leakcheck.
