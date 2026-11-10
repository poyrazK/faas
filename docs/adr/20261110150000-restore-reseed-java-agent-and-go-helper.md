# ADR-20261110150000: Java joins the restore reseed barrier; Go gets a helper and a warning

- Status: Accepted (owner decisions 2026-11-10)
- Date: 2026-11-10
- Amends: ADR-680/687 (restore userspace RNG reseed),
  ADR-20261109140000 (report processes the barrier missed)
- Related: ADR-20261107160000 (snapshot contract for plain apps)

## Context

ADR-680 reseeds Node and Python processes before a restored instance serves.
`TestMetalRestoreUncoveredRuntimeRNG` measured the other runtimes on the
native KVM host (ADR-20261107160000, 2026-11-09): two restores of one
snapshot returned the same Go `math/rand` and `math/rand/v2` values and the
same Java `java.util.Random` and `Math.random` values. Go `crypto/rand` and
Java's default `SecureRandom` read the kernel and differed.

The two runtimes differ in what can be done from outside the process:

- **Java** loads agents named in `JAVA_TOOL_OPTIONS` into every JVM. An agent
  can open `java.base` to itself through `Instrumentation` and reset the
  JDK's shared generators.
- **Go** has no hook. The global `math/rand/v2` generator lives in the runtime
  and has no reseed API, and since Go 1.24 the deprecated `math/rand.Seed`
  does nothing unless the program sets `GODEBUG=randseednop=0`.

## Decision

### Java: a reseed agent (owner choice)

1. guest-init embeds `rngpreload/java-reseed.jar`, writes it read-only under
   `/run/guest-init/rng/` with the other preloads, and appends
   `-javaagent:/run/guest-init/rng/java-reseed.jar` to the workload's
   `JAVA_TOOL_OPTIONS`, under the same rules as `NODE_OPTIONS`: only once the
   asset exists, never twice, not with `GREGALE_RESTORE_RESEED=off`.
2. The agent is compiled for Java 8 (class file 52) so every JVM can load it;
   a JVM refuses to start when an agent it cannot load is named. Unix-domain
   sockets need Java 16, so on older JVMs the agent does nothing; the
   coverage report (below) names those processes. Every agent failure is
   caught: the agent never stops a JVM from starting.
3. On Java 16+ the agent registers on the barrier socket from a daemon thread
   (`hello java <pid>`). On `reseed <nonce>` it mixes the nonce with
   `/dev/urandom` and resets:
   - `Math.random` and `StrictMath.random` (their shared `Random`);
   - `Random`'s seed uniquifier, so a `new Random()` after the restore does
     not repeat a sibling's;
   - `ThreadLocalRandom`'s seeder and the seed of every live thread that has
     used it;
   - `SplittableRandom`'s default generator.
   A field a JVM does not have is skipped; failing to reset one that exists
   answers `err`, which fails the resume closed and cold-boots the instance,
   as for Node and Python.
4. Not reachable: a `java.util.Random` (or `SHA1PRNG` `SecureRandom`) the
   application created before the snapshot. The docs keep it on the "reseed
   these yourself" list.
5. JVMs print `Picked up JAVA_TOOL_OPTIONS: …` on stderr at start. The docs
   say so; `GREGALE_RESTORE_RESEED=off` removes it with the reseed.

### Go: a helper and a warning (owner choice)

1. `github.com/poyrazK/faas/sdk/go/reseed`, imported for its side effect,
   registers the process on the barrier socket (`hello go <pid>`) and, on
   `reseed`, runs the callbacks the application registered with
   `reseed.OnRestore` before answering. An application reseeds its own
   generators there; a callback error or panic fails the resume closed. The
   package cannot reseed the global `math/rand` or `math/rand/v2`
   generators, and says so.
2. The coverage report (ADR-20261109140000) also names unregistered Go
   processes (executables carrying Go build info) and Java processes. Labels
   `go` and `java` join the closed runtime label set of
   `vmmd_restore_reseed_unregistered_total`. Restores are not refused.

## Consequences

- Java apps no longer replay `Math.random`, `ThreadLocalRandom` or a
  post-restore `new Random()` across restores or siblings. Restore latency
  grows by one local round trip per JVM, as for Node.
- A JVM older than 16 is reported, not reseeded.
- Go apps that use `crypto/rand` (the docs' advice) need nothing; others are
  reported on every wake until they register through the helper.
- Validation: unit tests for env stamping, the agent asset, the coverage
  classifier and the helper; the uncovered-runtime metal test asserts that
  Java's `Math.random` and `ThreadLocalRandom` now differ across restores.
