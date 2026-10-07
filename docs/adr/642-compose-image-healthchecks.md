# ADR-642: Compose healthchecks for prebuilt image workloads

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Stateless Compose services deployed from `image:` retain their
  `healthcheck:` declaration. Scan plans expose a typed partial override, app
  project metadata retains it, and image admission captures a versioned
  `image_healthcheck` contract in the deployment's existing profile. A captured
  null override explicitly inherits the immutable image's check. Legacy rows
  without the contract retain their app-metadata fallback.
- **Why:** Compose often supplies a check independently of the Dockerfile.
  Ignoring it loses declared behavior, while looking it up from the app at
  materialization time lets later source edits change queued releases.
- **Semantics:** String tests become `CMD-SHELL`; argv tests must use `CMD`,
  `CMD-SHELL`, or `NONE`. `disable: true` becomes `NONE`. Empty tests and zero
  timing/retry fields inherit image values, matching Docker's merge rules.
  Positive durations retain nanosecond precision and use the existing OCI
  minimum duration. The four durations are `interval`, `timeout`,
  `start_period`, and `start_interval`. `$$` is unescaped to a literal dollar;
  scanning does not resolve variables from the host environment. Invalid
  commands, durations, retries, and unsupported healthcheck fields fail scan
  without exposing command contents in the validation error.
- **Preservation:** Project apply, GitHub pushes and previews, and CI image
  publication capture the same contract before work becomes claimable. Runtime
  profile updates can neither replace it nor introduce it onto legacy rows.
  Existing profile-copy paths preserve it for stage retries and rollback.
  Removing the image declaration clears current app metadata while accepted
  image releases keep their captured check.
- **Follow-up:** [ADR-643](643-image-healthcheck-readiness.md) adds a fresh host
  readiness gate for newly assembled primary image deployments. The boundary
  below records the scope of the initial import change.
  [ADR-646](646-compose-dependency-release-gates.md) later adds captured
  `service_healthy` gates between separate project apps.
- **Boundaries:** This imports checks into the existing guest OCI healthcheck
  execution and startup/dependency behavior. It does not add a new host command
  readiness gate: main deployment traffic promotion retains TCP/HTTP readiness
  and authenticated public-route verification. A failing command probe alone
  is not guaranteed to prevent promotion of every single-workload image.
  Compose dependency conditions remain name-only project edges. Source-built
  Compose workloads retain their artifact healthchecks and warn that Compose
  overrides are not applied. No database migration, new VM lifecycle owner,
  or host execution of image commands is introduced.

Portable tests cover declaration validation, exact timing and image inheritance,
frozen metadata after app changes, image assembly, source/CI admission, guest
startup execution, and memory/PostgreSQL profile updates and retries. Native
Firecracker boot/restore qualification remains a separate acceptance gate.
