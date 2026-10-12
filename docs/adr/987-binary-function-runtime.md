# ADR-987: `binary` function runtime

- **Status:** accepted
- **Date:** 2026-10-11
- **Related:** ADR-003 (builder microVMs), ADR-052 (adding a runtime),
  ADR-687 (restore reseed barrier), §4.9 (function envelope)

## Context

Functions run only on interpreted runtimes and Go. Compiled languages
(Rust, Zig, C, C++) are a common ask for request handlers. They start
fast and use little memory, so they suit scale-to-zero functions. Today
they can only deploy as apps, which means writing an HTTP server and
paying its idle and wake costs.

Under ADR-052 a new language runtime costs a base image, a digest-pinned
publish, a runner, a builder plan, a migration and a support surface.
Repeating that per compiled language does not scale. Yet every compiled
handler needs the same thing from the platform: run one executable that
speaks the §4.9 envelope over stdin/stdout. The `go124` runner already
does exactly that for Go, including the persistent protocol and
restart-on-restore.

## Decision

1. **One runtime for every compiled language.** `binary` joins
   `api.FunctionRuntimes` and both runtime CHECKs (`apps.runtime`,
   `request_telemetry.guest_runtime`). It is a function runtime only;
   a long-running server with a Dockerfile is already an app.

2. **Built from the customer's Dockerfile.** A `binary` deploy needs a
   Dockerfile at the source root; apid rejects the deploy with 400
   `Dockerfile required` without one, and every other function runtime
   still rejects a Dockerfile. The build uses the existing BuildKit
   Dockerfile path inside the builder microVM (ADR-003), under the usual
   build caps. The CLI never infers `binary`: it is chosen with
   `--runtime binary`, `gregale.yaml` or the `function-rust` template.

3. **Keep one file.** imaged applies the `go124` source-build
   normalization: the image's `ENTRYPOINT` (or `CMD`) must name one
   literal executable inside `/app`. That file is copied to
   `/app/handler` as the whole app layer, and the rest of the image is
   discarded. The executable must therefore be statically linked.

4. **Alias, not a new base.** `api.FunctionBaseRuntime("binary")` is
   `go124`. Base keys, base refs (`FAAS_DEPLOY_BASE_REF_GO124`), staging
   and the runner path all resolve through it, so `binary` adds no base
   image, no publish and no disk. The runner is the `go124` runner started
   with `--runtime binary`. It reports `binary` in `FAAS_RUNTIME`,
   `X-Faas-Guest-Runtime`, request telemetry and the runner metrics, and
   registers with the restore barrier as `binary`.

5. **Same lifecycle as Go functions.** Executables that speak the
   persistent protocol are prestarted and reused. On every snapshot
   restore they are replaced before the instance serves traffic, because
   a compiled program has no in-place reseed hook (ADR-687). Other
   executables run once per request.

6. **No runtime release pin.** A Dockerfile build records no runtime base,
   so a `binary` deployment pins no runtime release, the same as other
   Dockerfile builds. The runtime-release enum is unchanged.

## Consequences

- Rust, Zig, C, C++ and statically linked Go functions deploy with no
  per-language platform work. The `function-rust` template is the
  reference.
- Runner and protocol fixes reach `binary` and `go124` together; a change
  to the `go124` base changes the base of every `binary` function too.
  A dynamically linked executable may run on that base today but is
  outside the contract.
- Each language's toolchain runs in the customer's Dockerfile, so build
  speed and cache use are the customer's, inside the 15-minute build VM.
- Runtime breakdowns in metrics and telemetry gain one label value
  (72 runner metric series).

## Alternatives

- **One runtime per language (`rust1`, `zig0`, …).** Rejected: every one
  repeats the ADR-052 cost for an identical guest contract.
- **Dockerfile apps instead of functions.** Rejected: customers write an
  HTTP server and lose the function envelope, triggers and per-request
  model.
- **A dedicated scratch base for `binary`.** Deferred: it would make the
  static-linking rule hard, but it needs its own published, digest-pinned
  base. The `go124` base costs nothing extra and already carries the
  runner.
