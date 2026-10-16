# ADR-1239 · Docker-compatible image process identity and root-owned files

- **Status:** accepted
- **Date:** 2026-10-14
- **Amends:** ADR-411 (numeric UID absent from passwd), ADR-136 §5 (root-owned
  layer entries)

## Context

Container deployments promise the Docker process model, but three behaviours
diverged from it:

1. **Root-owned files.** Layer extraction skips `chown` for entries owned by
   UID/GID 0 and leaves them under the imaged daemon's UID. ADR-136 recorded
   that those files "appear as UID 0", which held while imaged ran as root.
   imaged now runs as the unprivileged `faas-imaged` user with `CAP_CHOWN`,
   and `mkfs.ext4 -d` copies staging owners as-is. Root-owned image files
   (`/etc`, `/usr/bin`, application files) therefore reach the guest owned by
   the service user. Ownership checks fail (`sudo`, `sshd`, `cron`), and an
   image whose own service user shares that UID owns the system tree. An
   entry owned `1001:0` also lost its UID, because both IDs had to parse.
   The e2e and native acceptance harnesses run imaged as root and cannot
   observe this.
2. **No `USER`.** An image without `USER` ran as the platform app user
   (UID 1000). Docker runs it as root. Images such as the official nginx bind
   port 80 and write root-owned cache directories, and failed.
3. **Numeric UID absent from passwd.** ADR-411 gave such a UID the same
   primary GID. Docker uses GID 0. Images built for arbitrary UIDs (the
   OpenShift and Bitnami convention) make their writable paths group-0
   writable, and failed.

## Decision

1. When imaged holds `CAP_CHOWN` (or runs as root), layer extraction chowns
   entries owned by UID or GID 0 (name empty or `root`) to 0. Without the
   capability, for example an unprivileged development daemon, extraction
   keeps the previous behaviour instead of failing every build. imaged logs a
   warning at boot in that case.
2. A customer-supplied image (direct image deployments, project images,
   image-run jobs and sidecars) without `USER` runs as root (`0`). Source
   builds and platform runtimes keep the platform app user. The guest is the
   isolation boundary, and explicit `USER root` was already supported.
3. A numeric UID absent from the image's passwd runs with GID 0. A UID present
   in passwd keeps its passwd primary group, and an explicit group still wins.

## Consequences

Root inside the guest has the same reach an explicit `USER root` image already
had: the tenant's own VM. No host boundary changes. Images that relied on the
previous defaults (running as UID 1000 without `USER`, or GID equal to UID)
behave as they do under Docker after a redeploy. Existing artifacts keep their
ownership until the app is redeployed, so a redeploy is required to pick up
correct root ownership. Shared base images built by imaged pick it up on their
next rebuild.

## Validation and recovery

Unit tests cover root preservation on and off, the root default for images
without `USER`, and GID 0 for an unknown numeric UID. Native acceptance must
run imaged as the production service user to observe file ownership; the e2e
harness runs it as root. Recovery is a redeploy of the previous artifact.

Native evidence (gregale-internal-test-1, 2026-10-14):
`TestImageProcessIdentityMetal` passes in a real guest (an image without
`USER` runs as 0:0; `USER 1001` without a passwd entry runs as 1001:0).
`TestRootOwnershipPublishedByUnprivilegedBuilder`, run under `setpriv` as UID
65534 with only `CAP_CHOWN`, `CAP_DAC_OVERRIDE` and `CAP_FOWNER`, publishes
root-owned entries as 0:0 and a `1001:0` entry intact; without `CAP_CHOWN` the
chown to root fails, and plain `mkfs.ext4 -d` publishes the builder's own UID.
