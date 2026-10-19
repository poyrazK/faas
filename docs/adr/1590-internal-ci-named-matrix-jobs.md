# ADR-1590: Bounded named matrices for internal CI

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1571, ADR-1573, ADR-1588 and ADR-1589

## Decision

Jobs may declare a `matrix` mapping of one to eight named variants. Each variant
can override the job's digest-pinned `image` and literal, non-secret `env` values.
The base job still requires a pinned image. Empty variant mappings inherit the
base settings. There are no expressions, axes, substitutions or Cartesian products.

```yaml
version: 1
repository: gregale/example
jobs:
  test:
    image: registry.example/ci@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    env: {BUILD_MODE: test}
    matrix:
      alpha:
        env: {TEST_SUITE: alpha}
      beta:
        image: registry.example/ci@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
        env: {TEST_SUITE: beta}
    steps:
      - name: unit
        run: make test
    artifacts: [reports/**]
  collect:
    image: registry.example/ci@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    needs: [test]
    inputs:
      - name: report
        job: test-beta
        path: reports/unit.xml
    steps:
      - name: inspect
        run: test -s .gregale-ci-inputs/report
```

## Deterministic planning

Compile this example into three ordinary jobs: `test-alpha`, `test-beta`, and
`collect`. Concrete names are `JOB-VARIANT`, must be valid existing CI identifiers
(at most 40 ASCII bytes), and must not collide with any declared job name or
another expanded name. Names are never truncated. Variant map insertion order
cannot change names, dependency order, canonical definition digest or execution
configuration. Normalization clones job, step and variant maps before defaults
or sorting; each concrete job has independent copies.

The canonical revision retains the matrix declaration; the execution plan and
native commands contain only fully expanded jobs. The existing definition limit
of 16 jobs also applies after expansion, counting ordinary jobs and every variant.
The new per-job variant bound lives in `pkg/api/limits.go`. Capabilities advertise
`max_matrix_variants_per_job`; `max_jobs` includes expanded jobs.

Environment precedence is fixed runner values, job values, variant values, then
step overrides. Existing reserved-name, UTF-8, value/count/aggregate and effective
step bounds apply to every variant. YAML environment values must be strings.
Step commands, timeouts, working directories, artifact/JUnit selectors and input
declarations are inherited unchanged. Definition and native manifest byte limits
remain enforced at their existing boundaries.

## Dependencies and artifact inputs

A logical matrix dependency (`needs: [test]`) expands to all its concrete variants.
A concrete dependency (`needs: [test-beta]`) selects only that variant. Expansion
rejects unknown names, overlapping dependencies such as `[test, test-beta]`,
self-dependencies and cycles. Normal topological planning then applies.

An artifact input must name a concrete producer (`job: test-beta`), including
when the consumer depends on the entire matrix. A logical matrix name cannot
choose an arbitrary producer. The producer must be in the expanded direct
dependencies and select the requested literal artifact. Matrix consumers inherit
the same explicit input declarations; there is no implicit variant matching.

Existing execution-scoped input ownership pins the selected successful, sealed,
verified and stopped producer attempt. Each variant has independent attempt IDs,
log cursors, progress, artifact keys and native cleanup. A dependent waits for
all its required variants to complete native stop; a failing variant skips its
dependents while independent siblings continue. Explicit execution cancellation
still cancels all active variants and never starts waiting jobs. There is no
automatic fail-fast cancellation or retry of user commands.

## Templates, profiles and results

Admission and saved profiles require an exact template map keyed by concrete
job names. Each template must match that variant's resolved pinned image and the
existing account/plan/native policies. A qualified same-image template may be
reused under multiple concrete names; this does not reserve extra concurrency.
Existing active-execution limits, native queue/resource admission, attempt fencing
and retry prohibitions apply unchanged.

API status, CLI plans/status, logs, artifact/report lookup and the existing
pipeline GitHub Check summary use the concrete names. This creates separate job
results within the pipeline Check, without adding per-variant Check resources.
Restarts reuse frozen concrete jobs and attempts. Whole-pipeline reruns preserve
variant configuration/templates and create new execution/attempt/output identities.

## Compatibility and verification

The expanded plan uses the existing flat plan schema (version 1). A matrix does
not require a new guest feature: each concrete job uses manifest v1, v2 or v3
according to its actual inputs/environment/directory settings. Dispatch and guest
validation explicitly reject unexpanded matrix declarations. Unchanged definitions
retain their existing canonical serialization and feature behavior.

Upgrade definition readers/controllers before admitting matrices, and use runner
images qualified for the concrete jobs' existing manifest features. Keep admission
off during binary rollback and drain with current binaries; retained matrix
definitions require current readers for new admission/reruns. No migration or
additional mutable configuration record is introduced.

Portable tests exercise deterministic names/digests, limits/collisions, environment
precedence, dependencies/inputs, exact templates/profiles, PostgreSQL restart and
rerun identities, independent failures/cancellation, scoped HTTPS guest execution,
producer stop/seal and explicit variant artifact download. Planning tests never
execute repository commands.

Native/provider qualification remains with the separate agent: qualify each
pinned image, run variants through real admission, restart the controller, cancel
queued/running variants, and confirm per-variant output and native cleanup. The
frozen qualification bundle is untouched. The service stays internal, flags stay
off, and release stays paused; no image publication or deployment is included.
