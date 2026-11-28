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
   and build. builderd never records a build publication for an uploaded
   image, so its signature never vouches for bytes it did not build. If an
   app later turns on signed images, those deployments cannot satisfy the
   policy.
6. **Account spool budget.** Opening a source upload keeps the budget of 4×
   the source tarball cap. Opening an image archive may fill up to the larger
   of that and one image archive at the app layer cap, never 4× the image cap.
   On Scale that is 2 GB rather than 1 GB, and only for an image session.
7. **Rollout.** apid accepts image archives only for accounts the
   `local_image_deploys` platform flag releases. It checks the flag at session
   start and again at commit, so turning it off also stops open uploads. Turn it on only after every
   builderd runs this release. An older builderd would treat the archive as
   a source tarball, and an older apid ignores `source_format` and refuses the
   upload at commit because it is not gzip. Retries of an uploaded image are
   capped by the image cap, not the source cap.
8. **Converter details.** A name without a tag or digest gains Docker's
   implied `:latest` before `docker save`, so the CLI saves one image and not
   every tag of the repository; image IDs are left as they are. A layer repeated
   in a legacy save, stored once with the duplicate path as a link, reuses the
   staged layer with the same diff ID. Inspect keeps in memory only blobs that
   start with `{` and fit the config cap, at most a manifest plus a config in
   total; layers stream through.

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

## Amendment 1 · 2026-11-28 · Reuse the app's previous layers

A redeploy of a local image usually changes one small top layer, yet the CLI
uploads every layer again: hundreds of megabytes over the customer's link
for a one-line change. This amendment lets a redeploy skip the layers the
app's previous uploaded image already has.

1. **Scope: the same app.** Layers are reused only from the app's own previous
   uploaded image, never across apps or accounts. Nobody can learn whether
   another tenant's image holds a layer, and no shared blob store needs
   accounting or garbage collection.
2. **The base is the app's latest uploaded image.** After a commit enqueues
   a deployment, apid stores the full canonical archive it accepted as the
   app's **image base**, together with a small JSON summary: the manifest
   digest and each layer's digest and size. Each base replaces the one
   before it, so an app holds at most one archive, no larger than the plan's
   image cap (256 MB to 2 GB). With a remote storage backend
   (`FAAS_STORAGE_BACKEND` oci, gcs or s3) the keys are
   `image-bases/<app_id>.tar` and `image-bases/<app_id>.json`. A single-box
   install keeps them under the upload spool. They sit outside `sources/`, so
   the 24-hour source retention does not remove them. Purging an app deletes
   its base. A soft-deleted app keeps it, so a restore can reuse it.
   Storing the base is best effort: if it fails, the deploy goes on and the
   next upload sends every layer.
3. **The CLI asks, then sends a thin archive.** `GET /v1/apps/{app}/image-base`
   returns the summary to anyone who may deploy the app, or 404 when there is
   none. The CLI converts the image as before, leaves out the layer blobs the
   base lists with the same digest and size, and uploads the result. The
   `oci-layout`, `index.json`, manifest and config entries always stay.
   The upload sets `deploy_options.reuse_image_base = true` beside
   `source_format = "oci_image_archive"`. The session's size cap applies to
   the thin upload, and the plan's image cap applies again to the rebuilt
   archive.
4. **apid rebuilds the full archive at commit.** Before inspecting, apid
   fetches the base archive and writes a new canonical archive in the
   canonical order. Each layer comes from the upload when it carries the
   layer, otherwise from the base. Then the existing `Inspect` checks run on
   the result, which re-hashes every blob, and the full archive becomes the
   deployment's source. builderd and imaged see exactly what a full upload
   produces and do not change. A layer that is in neither the upload nor the
   current base (for example, another deploy replaced the base in between)
   refuses the commit with `409 image_base_changed`. The CLI then uploads the
   full archive. Without `reuse_image_base`, a missing blob is refused as
   before.
5. **Rollout.** This rides on the `local_image_deploys` flag. The CLI uses a
   thin archive only when the image-base endpoint answers. An older apid has
   no endpoint, so the CLI falls back to a full upload. An older CLI never
   sets `reuse_image_base` and always uploads in full.

Consequences: a redeploy uploads only the changed layers, at the cost of up to
one stored image per app and one internal fetch of that image on each reusing
commit. Internal transfer is cheap next to the customer's upload link.
