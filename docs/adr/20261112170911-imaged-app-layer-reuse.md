# ADR-20261112170911 · imaged reuses an app layer whose inputs are unchanged

- **Status:** Accepted (2026-11-12, owner: "Go on then").
- **Date:** 2026-11-12
- **Amends:** the per-deploy scan reuse in `pkg/imaged/scan_reuse.go`
  (#5794), whose lookup gains the deployment a layer was copied from.
  ADR-038 (every layer is signed and verified at cold boot), ADR-075 (an
  enforce-mode deployment carries its own scan) and
  ADR-20261028102540 (scan reuse by scanner identity) are unchanged.
- **Switch:** imaged `FAAS_IMAGED_LAYER_REUSE`, default off.

## Context

Production, the seven days to 2026-11-12 (`deployments.stage_state` and
imaged's "image preparation timing" log):

- **A build-cache hit still pays for the conversion.** 105 of 268
  successful source deploys (39%) were build-cache hits. Their p50 was 82 s:
  image build 46 s, security scan 12 s, snapshot preparation 20 s, source
  download 6 s. builderd skips the builder VM on a hit and hands imaged the
  cached OCI image. imaged then converts that image as if it were new: it
  unpacks the layers, runs syft, writes a new ext4 with `mkfs.ext4 -d`,
  hashes it and uploads it. On rc.339 (64 deploys after 11:00Z) imaged's
  layer phase was 27 s at the median and 38 s at p90, and its scan phase was
  17 s and 34 s.
- **The scan reuse by artifact digest almost never fires.** It hit 4 times in
  1,840 per-deploy scans. `mkfs.ext4` writes a random filesystem UUID, a
  random directory hash seed and the current time, so the same inputs never
  produce the same bytes, and the digest the reuse keys on always differs.
- **The inputs determine the layer.** `rootfs.Builder.Build` writes the OCI
  image's layers above the base (or above a shared runner base), guest-init
  as `/sbin/init`, the app manifest imaged computes (`/etc/faas/app.json`)
  and a passwd table. The plan sets the filesystem size and the passwd uid
  bound. Nothing else enters the file.

## Decision

1. **Layer input key.** For an eligible deployment (item 4), imaged computes
   a SHA-256 over, length-prefixed: a format version, the account ID, the OCI
   manifest digest of the built image, the shared runner base (its runtime,
   diff IDs and why the layer keeps more than `/app`, when one applies), the
   final app manifest as JSON, the digest of the guest-init binary and the
   plan. It computes the key after the manifest and the shared-runtime split
   are final, immediately before the layer build.
2. **Index.** imaged keeps one small JSON record per key on its node, under
   the artifact cache directory (`<cache>/layer-reuse`, or `layer-reuse`
   beside the apps root on a node without a cache). A record names the
   deployment that built the layer, its storage key, the artifact digest and
   size, the content bytes, the filesystem size, the SBOM key and the
   guest-init and runner digests. imaged writes a record after each fresh
   build of an eligible deployment. The index is a cache: losing it costs one
   build. A second file per artifact digest names the deployment whose scan
   covers those bytes (item 5).
3. **Reuse.** On a matching record, imaged publishes the recorded layer's
   bytes under the new deployment's key through the same path a fresh build
   uses. It streams them from the node's local copy (the artifact cache file
   or the local backend's file), hashes them while storage takes them, and
   signs the new key over that digest. The digest must equal the record's,
   or the reuse fails. The SBOM is copied to the new deployment's SBOM key.
   Every other step runs as before: the build digest pin, the runtime profile,
   the runner base, the rootfs stamp, replication and companion layers. If
   the source is no longer local, the digest differs or any step fails,
   imaged drops the record and builds the layer as today.
4. **Eligible deployments.** Source-build container deployments
   (`buildLocalOCIAppLayer`) without a signed source binding, and not sandbox
   templates. Functions, registry images, signed source builds and companion
   layers build as today.
5. **Scan.** A reused layer has its source's bytes, so its scan may reuse the
   source deployment's result under the existing rule: a complete scan of the
   same artifact digest, by the scanner and database of this node's last real
   scan, never for an enforce-mode app. The lookup, which covered the app's 20
   most recent deployments, also takes the deployment the digest file names,
   when that deployment belongs to the same account. imaged remembers the
   digest it computed while publishing, so the scan does not hash the layer
   again.
6. `imaged_app_layer_reuse_total{outcome}` counts hits, misses and fallbacks.

## Security

- **Tenancy.** The key and the scan lookup are bound to the account. A layer,
  its SBOM and its scan result never move between accounts.
- **Integrity.** The new key is published and signed exactly as a fresh build
  publishes and signs, over a digest computed from the bytes storage
  received. That digest must match the record. vmmd's signature verification
  at cold boot is unchanged.
- **Scope of the key.** It covers every input `Build` writes. A new image,
  manifest field, guest-init binary or plan changes the key, so the layer is
  rebuilt.
- **Evidence.** A reused scan is labelled with the deployment it came from
  (`reused_from`), as today. Enforce-mode apps scan every deployment.
- **Index trust.** The index lives in imaged's own cache directory, which
  already holds the artifacts it describes. A record only names bytes to
  republish. Their digest is checked before the new key is signed, so a
  wrong record costs a rebuild and cannot change what is signed.

## Consequences

- **Expected time saved on a build-cache hit:**
  - The layer phase drops from about 27 s to the work before the key (OCI
    digest, config, manifest) plus one local copy, hash and upload of the
    layer. That is a few seconds; the deploy-time histogram will show the
    actual figure.
  - When the scan reuse applies, the scan phase drops from about 17 s to
    staging.
- **A reused layer shares its source's filesystem UUID.** No guest mounts by
  UUID; drive1 is attached by position, and two app layers never share a VM.
- **The SBOM of a reused layer is the source's SBOM**, which describes the
  same files.
- **A record can outlive its source.** The source deployment's artifact can be
  collected, or evicted from the node's cache, while its record remains. The
  next reuse then fails over to a build and drops the record.
- **The index is per node.** A deploy handled by another node's imaged builds
  as today.
- **Not covered:**
  - Functions, registry images and signed source builds.
  - Sharing the index between nodes.
  - The scanner identity probe (ADR-20261028102540) as the reuse
    identity. Today a scan is reused only within an hour of a real scan on
    the node.
