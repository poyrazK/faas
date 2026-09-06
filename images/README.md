# images/ — Dockerfiles for content-addressed base images (spec §4.6, §15)

`base-debian-parent`, `base-minimal`, `runner-node22`, `runner-node24`, `runner-python312`, `runner-python313`, `runner-go124`, `runner-go124-alpine`, `builder-base`. Built in CI,
staged to /srv/fc/base/ (inside the 60 GB reserve, counted once). drive0 is one of
these shared read-only base rootfs; per-app layers stack over it via overlayfs.
Never flatten into one rootfs per app (breaks the 130 MB fleet target).

`runner-python313` is a standalone Wolfi/glibc chain rather than a child of
`base-debian-parent`; imaged stages its complete shared base once and still
uses the same two-drive layout for every Python 3.13 app.

The runtime/base images are published under `ghcr.io/poyrazk/<image>` by the
`runtime-bases` matrix in `.github/workflows/images.yml`. `builder-base` keeps
its separate multi-arch publication job. Runtime Dockerfiles are pinned to
linux/amd64 child digests because `imaged` rejects manifest-list references at
staging time.

Every matrix job validates the concrete OCI artifact it built, runs the
runtime contract smoke, and runs the shared Grype gate before promoting the
`latest` or `sha-*` tags. The hardened Python 3.13 lane rejects every CRITICAL
finding to match vmmd's fail-closed boot gate, including vendor-unfixed
findings. Legacy runtime lanes retain the fixable-CRITICAL gate until their
base migrations land separately. The builder job uses a fixed-finding HIGH
gate because it ships the BuildKit daemon and client, and performs those checks
for both linux/amd64 and linux/arm64. BuildKit, Railpack, mise, crane, and the
BuildKit source archive are checksum-verified before they enter the build or image
verification path.

For local images, use `make scan-images IMAGE_REFS="name:tag ..."`. Scanning
`dir:images/` only scans source files and is not an OCI image vulnerability
check.
