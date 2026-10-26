# ADR-20261025114554 · Anonymous Docker Hub pulls go through operator mirrors

- **Status:** accepted
- **Date:** 2026-10-25
- **Amends:** ADR-062 (private-registry pulls: a credentialed pull never uses a mirror)
- **Decision:** `FAAS_DOCKER_HUB_MIRRORS` names up to four registry hosts that serve Docker Hub content: `mirror.gcr.io`, or a pull-through cache. Set it with the `faas_docker_hub_mirrors` inventory variable; ansible renders a drop-in for imaged and builderd on compute nodes.
  - **imaged** tries each mirror in order, then Docker Hub, for every anonymous `docker.io` read: tag and digest resolution, manifests, configs and blobs (`oci.WithDockerHubMirrors`). Any mirror failure moves the read on to the next source and logs a warning. A cancelled pull stops.
  - **Builder VMs** get the list in the build manifest (`docker_hub_mirrors`). guest-init writes `/etc/buildkit/buildkitd.toml` with `[registry."docker.io"] mirrors = […]` and starts BuildKit with it. Dockerfile `FROM` lines and Railpack base images then use the mirrors, and BuildKit falls back to Docker Hub itself.
  - **Never with a credential.** A pull that carries an app's or a run's registry credential goes to Docker Hub only, so a credential is never sent to a mirror. Other registries are never mirrored.
  - **Unchanged identity.** Resolved references, deployment records and error messages name `docker.io`, not the mirror.
  - **Validation.** Each entry is a lowercase host with an optional port, reached over HTTPS: no scheme, path or Docker Hub host. An invalid list fails imaged and builderd at startup. guest-init validates the list again before writing it into TOML.
- **Why:** Docker Hub limits anonymous pulls per IP: 100 per 6 hours before April 2025, and 10 per hour since. A compute node pulls from one IP for every customer, and every builder VM starts with an empty BuildKit cache, so a few Dockerfile builds of `FROM node:22` or deploys of `nginx` per hour exhausted the limit. Pulls then failed with HTTP 429, and retries did not help. The node's blob cache does not avoid this, because manifest reads, which are what Docker Hub counts, still reach Docker Hub.

## Trust

- **Mirrors are trusted the way Docker's `registry-mirrors` are.** Blobs, and manifests pinned by digest, are checked against their digests whichever host serves them, so a mirror cannot alter them.
- **A mirror decides what a tag resolves to.** Production images should be pinned by digest (`docs/registry-credentials.md`). A mirror's cache can be stale for a tag Docker Hub just moved; deploying by digest avoids that.
- **Mirrors pass the tenant egress guard.** imaged reaches them through the egress-guarded client, so a mirror on a private address (RFC1918, link-local) is refused. Builder VMs are under the tenant egress rules too.

## Consequences

- With `faas_docker_hub_mirrors: mirror.gcr.io`, deploys and builds of Docker Hub images no longer depend on Docker Hub's per-IP allowance. Private Docker Hub repositories pulled with a stored credential still count against that credential's own allowance.
- A warm builder keeps the BuildKit daemon it started with. A changed mirror list reaches it when its warm slot is replaced.
- Rolling upgrades: an older guest-init ignores the manifest field and pulls from Docker Hub, as before. An older builderd sends no list.
- Not covered: platform-wide Docker Hub credentials, mirrors for registries other than Docker Hub, and private (RFC1918) pull-through caches.
