# ADR-20261028084018 · Source builds export zstd layers

- **Status:** Proposed
- **Date:** 2026-10-28
- **Amends:** the source-build artifact handoff from builderd to imaged
  (spec §4.5). Registry images, the shared base pipeline and the scanners are
  unchanged.
- **Switch:** builderd `FAAS_BUILD_LAYER_ZSTD`, default off.

## Context

A source build runs BuildKit inside a 2-vCPU builder VM. It exports two
things, and BuildKit gzips both by default:

- the OCI image (`--output type=oci`), which imaged verifies, filters and turns
  into the app's ext4 layer;
- the app's dependency cache (`--export-cache type=local,mode=max`), which
  writes every layer, base image layers included, and lives on the node for
  the next build.

Production on 2026-10-27 (rc.297):

- **The export dominates a cold build.** A cold Node build spent 276 s in the
  VM. Its final step, exporting the image and the cache, took 147 s.
- **gzip is the slow part.** One real dependency-cache layer from production
  (229 MB compressed, 618 MB raw), recompressed on one production core:

  | Codec | Compress | Size | Decompress |
  |---|---|---|---|
  | gzip -6 | 31.1 s | 224.5 MB | 5.6 s |
  | zstd -3 | 4.9 s | 207.7 MB | 1.2 s |
  | zstd -1 | 3.8 s | 235.8 MB | — |

- **imaged pays gzip twice more.** It gunzips each layer to verify its DiffID,
  gunzips it again to keep only `/app` for functions and shared-runtime apps,
  gzips the result into a temporary file, and gunzips that once more while
  building the ext4. Its layer phase took 38 s at the median.
- **Uncompressed is not an option.** The dependency caches share the 100 GB
  build disk (13 GB of them on one node) with the snapshot cache, and builds
  already fail when that disk runs short. zstd level 3 is smaller than gzip.

## Decision

1. **imaged and rootfs read zstd layers.** One opener in `pkg/rootfs` reads a
   layer as gzip or zstd from its magic bytes, so every layer-application
   path accepts both. Local OCI verification accepts
   `application/vnd.oci.image.layer.v1.tar+zstd` and checks that each layer's
   bytes are compressed as its media type says. The zstd decoder window is
   bounded by `api.LocalOCIZstdMaxWindowBytes` (128 MiB). Plain tar is still
   refused. Registry images keep their gzip-only media types.
2. **imaged's own temporary layers use zstd.** The `/app` filter and the Go
   handler layer are written once and read once on the node.
3. **Builders export zstd behind a switch.** With `FAAS_BUILD_LAYER_ZSTD=true`,
   builderd sets `BuildManifest.LayerCompression = "zstd"`, and guest-init adds
   `compression=zstd` to the OCI output and the dependency-cache export.
   `force-compression` stays off, so layers inherited from a gzip base image
   are reused rather than recompressed, and one image may mix both. The value
   also enters the build cache key, so turning the switch off never serves a
   cached zstd image.

## Rollout

The builder's guest-init and imaged release independently, so the order is:

1. Release imaged with step 1 everywhere. Nothing produces zstd yet.
2. Turn on `FAAS_BUILD_LAYER_ZSTD` for builderd node by node with a systemd
   drop-in. An older guest ignores the manifest field and keeps gzip.
3. Before rolling imaged back past step 1 on a node, turn the switch off
   there.

Measure on the first node: the builder's "build in VM" time for cold and warm
builds, imaged's `layer_ms`, and the dependency-cache size on disk.

## Consequences

- Cold builds should lose most of the export step's gzip time, and imaged's
  layer phase most of its decompression time. The dependency caches stay the
  same size or shrink.
- Customers' images do not change: imaged still produces an ext4 layer.
- Not covered: pre-seeding the Railpack frontend and base image into the
  builder (about 10 s per build), and accepting zstd from registries.
