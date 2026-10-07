# ADR-644: Image healthchecks drive runtime recovery

- **Status:** implemented; native lifecycle qualification pending
- **Date:** 2026-10-07
- **Problem:** ADR-643 gates serving startup on a fresh command success, but a
  main image's later command failures did not reach scheduler recovery. An
  HTTP listener could remain open while its declared check failed.
- **Decision:** A required primary image check also runs throughout each
  serving VM's lifetime. Vmmd initiates one fresh command attempt per effective
  interval and counts consecutive failures using the image's retry threshold.
  Success and startup-grace results clear the failure streak. The command
  replaces implicit plan-default HTTP liveness, so images do not need an
  invented /healthz endpoint. Explicit HTTP/gRPC liveness overrides remain
  independent additional probes. Declaring a command check enables its monitor
  even when ordinary HTTP liveness is disabled.
- **Fresh proof:** STREAM messages 16/17 read effective timing and a main-process
  incarnation; messages 14/15 execute one command. Every exchange uses a new
  host challenge over the instance's private Unix-vsock proxy. Each attempt
  must match both challenge and process incarnation. A process replacement
  clears prior failures and obtains a new configuration. No cached or
  unsolicited report counts as a command result.
- **Execution:** The ADR-643 runtime preserves identity, open workload cgroup,
  working directory, runtime environment, and refreshed secret bindings.
  Single-attempt requests retain command timeouts and startup grace without
  repeating the image's retries inside each request. Nanosecond intervals,
  including startup intervals, remain exact. Host transport gets a separate
  bounded allowance. Negotiating host monitoring stops the legacy DGRAM poll,
  avoiding duplicate recurring command execution.
  Host disconnect cancels and joins an in-flight command. Accepted probe
  sockets use close-on-exec so workload children cannot retain the connection
  and hide its disconnect.
- **Recovery ownership:** Reaching the declared failure threshold emits the
  stable reason image_healthcheck_unhealthy through the existing durable,
  node-bound liveness failure delivery. Schedd owns snapshot invalidation,
  confirmed VM teardown, STOPPED transition, routing invalidation, admission
  release, and normal request/service/worker recovery. Recovery waits for
  successful invalidation of both snapshot tiers; a failed invalidation retains
  resident ownership and the report retries. The next wake cold-boots.
  Confirmed command failures use existing restart accounting and exhaustion
  limits. Recovery does not immediately fail a live deployment after one
  transient command failure.
- **Infrastructure:** Missing, malformed, stale, or contradictory results
  break the command failure streak. Three consecutive transport/contract misses
  request existing infrastructure recovery; they do not consume the app's
  permanent-eviction budget. Cancellation during park, destroy, daemon shutdown,
  or sibling liveness recovery cannot emit a new failure.
- **Lifecycle:** The existing liveness registry owns both monitors under one
  daemon lifecycle context. Teardown cancels both; serving resume creates fresh
  monitor state. Paused reservations do not run serving monitors.
- **Compatibility:** Ping and serving boot/restore, warm resume, migration, and
  dedicated qualification acknowledgements advertise runtime monitoring.
  Schedd rejects readiness-only daemons before boot and cleans up contradictory
  serving responses. Guest configuration proof is required before readiness,
  so an older guest cannot silently omit the monitor. Roll out matching guest
  assets and vmmd before schedd; reassemble older image rootfs releases to gain
  the new guest protocol. Deployments without the effective command-check
  receipt keep their existing readiness/liveness behavior.
- **Diagnostics:** Audit/state-transition reasons distinguish confirmed command
  failure from infrastructure recovery. Existing probe metrics use
  image_healthy, image_unhealthy, image_starting, and image_transport.
  Frames and diagnostics contain no command output, arguments, or secret values.

Portable regressions cover fresh single command execution, exact timing,
startup grace and timeouts, failure-streak resets, process replacement, stale
wire responses, monitor cancellation, daemon compatibility, and scheduler
snapshot invalidation/retry with idempotent restart accounting.

Native qualification extends TestMetalImageHealthcheckAcrossSnapshots with a
still-listening guest that becomes unhealthy and requests runtime recovery.
Run on the isolated native x86_64 KVM host described in
[ADR-643](643-image-healthcheck-readiness.md):

~~~sh
FAAS_TEST_IMAGE_HEALTHCHECK=1 \
RUN_REGEX='^TestMetalImageHealthcheckAcrossSnapshots$' \
make test-metal PKGS=./pkg/fcvm
make leakcheck
~~~

Portable transport fixtures do not qualify native execution or lifecycle leaks.
