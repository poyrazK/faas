# ADR-20261127120000 · Deploy a local image without a registry

- **Status:** accepted
- **Date:** 2026-11-27
- **Scope:** `cmd/gregale` (`deploy --local-image`, `deploy --image-archive`), `pkg/ociarchive`, apid upload sessions, builderd
- **Related:** ADR-003 (builds run in builder microVMs), ADR-791 (build VM limits), ADR-20261024225612 (registry credentials)

## Context

`gregale deploy --image` takes an immutable registry reference
(`REF@sha256:<digest>`). A customer who has built an image with
`docker build` must first push it to a registry Gregale can pull from, and
for a private registry also set up pull credentials. That push is the most
common reason an existing container never reaches Gregale: people trying the
platform often have an image on their laptop and no registry at all.

The deploy pipeline already handles OCI images that are not in a registry.
A Dockerfile build leaves an OCI layout tar at
`<build-export-dir>/<build-id>/build/out/image.tar`, and imaged builds the app
layer from that file. The resumable upload session (`/v1/uploads`) already
moves large customer bytes to apid, resumably and within a size cap.

## Decision

1. **The CLI converts and uploads.** `deploy --local-image NAME` runs
   `docker image save` (or the client named by `GREGALE_DOCKER`, such as
   podman). `deploy --image-archive PATH` takes a file someone already saved.
   The CLI converts the `docker save` output (legacy `manifest.json` or the
   Docker 25+ OCI layout) into a **canonical archive**. The canonical archive
   is an OCI image layout tar that holds:
   - `oci-layout`;
   - an `index.json` with exactly one manifest;
   - `blobs/sha256/*`, with the files in a fixed order and mtime 0.

   Uncompressed layers are gzipped deterministically after their diffID is
   checked. Gzip and zstd layers are copied as they are. The image must be
   linux/amd64.
2. **One upload format flag.** The upload session gains
   `deploy_options.source_format = "oci_image_archive"` and an informational
   `image_name`. Everything else that `--image` deploys carry (healthcheck,
   scope, environment, reason, companions, release scheduling, rollback policy)
   rides on the existing upload deploy options. Every option that selects or
   shapes a source build is refused alongside it, at session start: `runtime`,
   `handler`, `dockerfile`, `source_root`, `source_url` and `commit_sha`.
3. **The cap is the app layer cap.** An image archive may be as large as the
   plan's app layer (`ImageArchiveMaxMB` = `AppLayerMaxMB`: 256 MB, 512 MB,
   1 GB or 2 GB), not the source tarball cap. The archive is compressed, so
   anything larger could never fit the layer it unpacks into. imaged still
   enforces the layer cap and the extraction budget on the unpacked image.
4. **apid inspects, builderd re-inspects, nothing runs.** At commit, apid
   checks the archive with `ociarchive.Inspect`. That is a streaming pass that:
   - allows only the canonical entry names;
   - hashes every blob against its name;
   - requires one manifest, a linux/amd64 config, and gzip or zstd layers
     whose sizes match.

   apid then enqueues a `dockerfile` deployment. Its inferred profile carries
   `uploaded_image` (manifest digest and name), the single exposed port and TCP
   readiness, as direct `--image` deploys do. builderd sees `uploaded_image`
   and skips the builder VM. It inspects the archive again and requires the
   manifest digest that apid accepted. It then places the archive at the
   canonical build export path and completes the build the way a cache hit
   does. imaged needs no change.
5. **Guards.** These uploads are refused, at session start and again at
   commit:
   - apps that require signed images (`require_signed` or a security policy
     that requires them), because builderd's build signature must never vouch
     for bytes it did not build;
   - function apps;
   - image names the stateful-datastore denylist matches.

   builderd refuses signed apps too, in case the policy changes between commit
   and build.

## Consequences

- A local image deploys with one command and no registry or pull secret.
- No customer code runs on the host or in a builder VM for these deploys. The
  only host work is the hash-and-parse pass in apid and builderd, the same
  file handling the source tarball path does. ADR-003 holds: nothing is built.
- These deploys use the upload bandwidth of the customer's link, which is
  slower than a registry pull from a nearby mirror for large images. Registry
  deploys remain the recommended path for CI.
- A retried deployment copies the inferred profile, so a retry also skips the
  builder VM.
- arm64 images are refused, as they are for registry deploys.
- Signed-image apps keep the registry-only path. Supporting them would need
  client-side signatures that builderd verifies, which is out of scope here.
