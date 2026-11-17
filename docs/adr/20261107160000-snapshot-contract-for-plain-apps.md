# ADR-20261107160000: Snapshot contract for apps that do nothing special

Date: 2026-11-07
Status: Accepted
Amends: ADR-022 (resume hook payload), ADR-192 (env.json keeps the capture's identity)
Related: ADR-680 (userspace reseed), ADR-222 / ADR-505 (secret reload), ADR-348 (after_restore)

## Context

A design review of the ChronoLake field report asked whether an ordinary app,
one that reads `process.env` once at startup and implements no lifecycle
hooks, behaves correctly when it wakes from a snapshot. Reading the code
showed three ways the platform's actual behavior and its documented contract
differed from what a plain app needs.

1. **Secrets: the docs were wrong, the platform was right.** The deployment
   prime (`pkg/sched/deployment_prime_boot.go`) boots with the app's sealed
   secrets and records the secret versions it used, and every secret change
   invalidates existing snapshots (state conformance
   `project_secret_change_invalidates_member_snapshots`). A restored process
   therefore has the environment a cold boot would give it. #6683 added a
   `gregale doctor` warning, `after_restore_without_secret_reload`, and a
   `docs/secrets.md` paragraph saying the snapshot has "no bindings or
   secrets" and that apps declaring `after_restore` must adopt the reload
   label or fall back to cold boots. That told developers to build the
   SIGHUP/reload contract when they did not need it.
2. **Instance identity in the environment is stale after a restore.**
   `FAAS_INSTANCE_ID`, `FAAS_NODE_ID` and `FAAS_REGION` are appended to
   env.json at wake (`appendPlatformIdentity`). A restored process keeps the
   capture's values, so every instance restored from one snapshot reports the
   same instance ID. ADR-192 deliberately keeps env.json on the drive
   consistent with the restored process, so the file cannot carry the new
   value either. The only documented workaround was per-request headers,
   which background code does not have.
3. **Userspace reseed coverage is unmeasured outside Node and Python.**
   ADR-680 lists Go `math/rand`, Java `Random`/`SecureRandom`, Ruby and PHP
   as not covered, without a probe that shows which of them replay.

## Decision

### 1. Plain apps need no secret code

The secret contract stated in `docs/secrets.md` is: the snapshot carries the
secret values current at capture; a change invalidates it; the next wake
cold-boots and re-captures. The reload label is only for applying a change
to processes that are already running. The `after_restore_without_secret_reload`
doctor check is removed. An `after_restore` handler must not depend on
`FAAS_SECRETS_FILE` or `FAAS_SECRETS_SNAPSHOT_FILE` unless the image sets
the label, because those exist only in reload-enabled images.

### 2. The serving identity is published in a file, rewritten on every resume

- vmmd's resume-hook payload gains an optional `identity` object
  (`instanceId`, `nodeId`, `region`). The manager reads it from the wake's
  prepared api env, attaches it to the wake context (like the traceparent),
  and records it on the instance, so a later resume of the same instance
  (frozen thaw, warm-pool promotion) carries it too.
- guest-init writes `/run/guest-init/identity.json` at boot from env.json and
  replaces it atomically on every resume, before the userspace reseed and the
  app's `after_restore` callback. The document is
  `{"instance_id","node_id","region","source","updated_at"}`; `source` is
  `boot` or `resume`. It is mode 0444 on tmpfs and holds no credentials.
- Workloads get `FAAS_INSTANCE_IDENTITY_FILE` (platform-owned, replaces any
  workload value). Sidecars that run from their own root filesystem get
  neither the file nor the variable, as with the ADR-680 preload.
- The environment variables are unchanged. They keep meaning "the instance
  that booted this process"; removing them would break apps that read them
  today for values that stay correct on a cold boot.
- A failed write does not NACK the resume: identity is not a uniqueness
  property, and refusing would turn a tmpfs error into cold boots. The file
  then keeps the previous identity, which the guest did serve as.
- Compatibility: an older vmmd sends no `identity` and the file keeps its
  last value. An older guest ignores the field.
- The instance's identity is not in the adoption block. After a vmmd
  restart an adopted guest's resumes carry none, and its file keeps the last
  identity published, which is the same instance. The exception is a
  warm-pool spare that was never promoted before the restart: once promoted,
  its file keeps the capture's identity until its next restore.

### 3. Measure the uncovered runtimes

`TestMetalRestoreUncoveredRuntimeRNG` restores one snapshot of a Go probe
and a Java probe twice. It fails if a generator that reads the kernel on
each call (Go `crypto/rand`) repeats, and logs whether each documented-gap
generator (Go `math/rand` and `math/rand/v2`, Java `Random`, `SecureRandom`
and `Math.random`) replayed. Whether to extend the reseed barrier to those
runtimes is decided from that evidence, not here.

Measured 2026-11-09 on the native KVM test host (Firecracker, kernel 6.1,
Go 1.26 probe on Alpine 3.20, Temurin JDK 21):

| Generator | Two restores of one snapshot |
| --- | --- |
| Go `crypto/rand` | differed (asserted) |
| Go `math/rand`, `math/rand/v2` | replayed |
| Java `SecureRandom` (default `NativePRNG`) | differed |
| Java `java.util.Random`, `Math.random` | replayed |

Java's default `SecureRandom` on Linux mixes `/dev/urandom` into every draw,
so it does not replay; an explicitly seeded or `SHA1PRNG` instance has no such
mixing and is still the app's to reseed. The docs list what replayed.

## Consequences

- The secret docs no longer ask plain apps for lifecycle work.
- Code outside a request can read the serving instance correctly after a
  restore. The per-request headers remain the request-scoped source.
- The resume body grows by about 100 bytes, well inside the 8 KiB cap.
- No database, API or SDK change. Running guests gain the file when they are
  next replaced by a guest-init carrying this change.

## Validation

- Unit: api-env parsing and context attachment; a restore wake's hook
  carries the wake's identity (fake VMM); guest boot, resume, absent-instance
  and older-vmmd cases; env stamping replaces a workload value; the resume
  connection publishes the identity before acknowledging (Linux).
- Metal: the uncovered-runtime probe above. Native x86_64 KVM restore and
  leakcheck before release, since the resume hook is on the restore path.

## Rejected alternatives

- **Blank the identity variables for snapshot-backed apps.** Never wrong, but
  breaks every app that reads them now, including on cold boots where they
  are correct.
- **Rewrite env.json on restore.** ADR-192 showed the rewrite costs a
  pre-boot drive session per restore and makes disk disagree with the
  restored process, and guest-init reads env.json only at boot.
- **Serve identity from `/v1/metadata/env`.** That endpoint proxies customer
  configuration from the database over vsock on each call; identity is per
  instance and known locally, and a file needs no HTTP client.
