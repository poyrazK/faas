# ADR-387 · Image healthcheck nanosecond timing

- **Status:** accepted
- **Date:** 2026-09-30

## Context

The image parser treated Docker HEALTHCHECK duration integers as seconds.
Docker image configs encode them in nanoseconds, as defined by the
[Docker image specification](https://github.com/moby/docker-image-spec/blob/main/spec.md).
A normal one-second interval therefore became one billion seconds. Existing
parser fixtures used the same incorrect units and concealed the bug.

## Decision

Decode image Interval, Timeout, StartPeriod, and StartInterval as nanosecond
`time.Duration` values. Both local and registry parsing use the shared decoder.
Reject negative durations, non-integer or overflowing JSON numbers, nonzero
values below Docker's one-millisecond minimum, and negative retry counts.
Do not guess that small wire integers are seconds.

Preserve exact image timings in the manifest's optional `healthcheck.image_timing`
object. Keep the existing second-based fields as a rounded-up compatibility
view for existing tools and older guest artifacts. New guests use exact image
timings whenever present, including subsecond values. Legacy manifests retain
their second-based behavior. Keep customer probe override fields in seconds;
`image_timing` is reserved for image metadata and rejected in companion probe
requests.

Main-image polling and companion image probes consume exact interval, timeout,
and startup grace. New image startup gates honor the retry budget and do not
charge failures during grace; a transient first failure must not kill the
workload immediately. Legacy first-probe behavior is retained for older
second-based main manifests. Polling uses an explicit image StartInterval during grace,
or Docker's five-second default when omitted. Existing typed/legacy deployment
probe cadence remains unchanged. A second-based deployment startup-grace
override updates the corresponding exact image timing without mutating the
base image manifest or changing unrelated timing. Validate conversion bounds
before multiplying override seconds into nanoseconds.

## Validation and rollout

Portable tests cover nanosecond image parsing through both readers, subsecond
manifest projection, invalid units/ranges, metadata-reserved override rejection,
and deployment grace precedence with defensive copies. Linux guest tests cover
exact runtime durations, companion propagation, and startup polling cadence.

The shell-free direct-OCI fixture posts actual probe identity, working directory,
fixed image/deployment markers, and cgroup membership back to its application.
Native tests require increasing probe counts before park and after restore,
with and without a companion. Snapshot-carried evidence alone cannot satisfy
the post-restore polling check. The container CI lane derives these tests from
the source and rejects skips.

Deploy the new imaged and guest artifacts and redeploy affected images so their
per-deployment manifests are regenerated. Older manifests are not rewritten by
this change. Linux execution and native microVM acceptance remain necessary
before claiming fleet qualification. Snapshot cold fallback and component
ownership are unchanged.

Image startup grace is evaluated at the start of each probe, following
[Moby's health result handling](https://github.com/moby/moby/blob/master/daemon/health.go).
A failure that completes after grace ends remains exempt when its probe started
within grace. Main polling, companion liveness, and startup gating share this
rule. Legacy manifests keep result-time grace semantics. Linux tests cover the
boundary and a slow failed probe followed by a successful retry.
