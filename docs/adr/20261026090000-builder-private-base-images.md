# ADR-20261026090000 · Dockerfile builds pull private base images with the app's registry credentials

- **Status:** accepted
- **Date:** 2026-10-26
- **Amends:**
  - [ADR 20261022111812](20261022111812-seal-domains-per-reader.md): vmmd joins imaged as a reader of the `build` domain.
  - ADR-062: apid is still the only writer of `app_registry_credentials`. builderd now also reads the ciphertext, and vmmd opens it.
  - [ADR-20261022185329](20261022185329-compose-build-secrets.md): build secrets and registry logins share the builder's `secrets.env`.
- **Decision:** a Dockerfile build pulls its `FROM` images with the registry credentials stored on the app it builds (`gregale registry set --app`).
  - **builderd** reads the app's `app_registry_credentials` rows (registry, username, sealed password) for every Dockerfile build. It forwards them unopened on `BuildSpec.registry_credentials` and logs the registry hosts, never usernames or passwords.
  - **vmmd** opens each password with the build seal domain (then the fleet and host identities, for passwords sealed before the domain), under the `registry_creds` namespace. It renders a Docker `config.json` (Docker Hub keyed by `https://index.docker.io/v1/`) and adds it to the builder's `secrets.env` as `GREGALE_BUILD_REGISTRY_AUTH`, after the build's own secrets. A password that does not open is logged and left out: that registry is pulled anonymously, as before. App VMs never get the entry.
  - **guest-init** (build mode) removes the entry from the secrets it read, writes it as `config.json` on the RAM-backed `/tmp` (directory 0700, file 0600), sets `DOCKER_CONFIG` for `buildctl`, and deletes `secrets.env` as it already did for build secrets. The entry is never mounted as a build secret.
  - **ECR access keys** (an `AKIA…` username for an ECR host) are not forwarded. Pulls exchange them for ECR's password (ADR-20261025124445), which a builder VM cannot do, and the key itself must never reach a registry. The build log says so; an `AWS` username with an ECR password works.
  - **No warm builder:** a build with registry credentials never restores or leaves a warm builder, as for build secrets, so a password never outlives its build in a snapshot.
  - **Separate cache:** such a build's cache key gains `registry_auth_scope`, a hash of the app and the sealed credentials. An image built `FROM` a private base is reused only by that app, and a replaced credential rebuilds. Builds without credentials keep their cache keys.
- **Why:** a Dockerfile whose `FROM` names a private image (a company base image on GHCR, ECR or Artifact Registry) failed with an authorization error. The app's registry credentials were used only when imaged pulled a deploy image.

## Why vmmd, not a new path through imaged

- vmmd already opens build secrets into the builder's `secrets.env`, so registry logins take the same staging, deletion and snapshot rules.
- The seal-domain ADR's threat table puts the `build` identity on every compute node and within reach of root there. vmmd is that root daemon and could already read the file, so this adds no holder outside compute nodes and does not widen what a vmmd compromise yields.
- The rejected alternative has imaged pre-pull private base images and hand them to BuildKit as OCI layouts. It keeps credentials out of the builder, but it would need builderd to parse `FROM` lines (including `ARG` substitution and multi-stage builds) and a new imaged-to-builderd handoff.

## Consequences

- A Dockerfile build sees the app's registry credentials inside its builder VM, as it sees its build secrets. Only the app's own build runs there, and `RUN` steps do not see `DOCKER_CONFIG`: buildctl answers BuildKit's registry auth over its session.
- Apps with registry credentials lose warm builders for Dockerfile builds.
- Rolling upgrades:
  - An older vmmd ignores the new field, and the build pulls anonymously, as before.
  - An older guest-init leaves the entry unused in `secrets.env`, deleting it only when the build declares secrets.
  - An older builderd sends nothing.
- Railpack builds are unchanged: their base images are public.
