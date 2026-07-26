# ADR-038 — build attestation for produced app layers

Status: Accepted, 2026-07-26. Owner: @poyrazK. Closes: issue #197 B3.1,
B3.2, B3.4, B3.9, B3.10-read. Related: PR #241 (Tier 3 Phase 1 — schema
half), the in-flight Phase 3 PR (cosign signer + verifier), ADR-039
(proposed — KMS-backed signing).

## Context

The base + app layer ext4 is the trust root of every cold-boot
(spec §4.6, two-drive rootfs). Today, nothing verifies that an ext4
attached to a Firecracker microVM came from the build pipeline we
shipped — a compromised `imaged` binary or a malicious admin could
swap an ext4 in `/var/lib/faas/apps/<slug>/` and `schedd` would happily
attach it. The current `builds` row records `started_at`,
`framework`, `source_sha256`, but not `buildkit_version`,
`railpack_version`, `base_digest`, `source_url`, `plan`, or
`builder_node_id` — auditors (regulator, postmortem, security
questionnaire) cannot answer "what actually ran?" without joining
three tables and grepping the build log.

Tier 3 Phase 1 (PR #241) shipped the schema half: `deployments` gained
`source_url` + `commit_sha` so the consumer-side inputs are
stamped. Phase 3 (in flight) ships the cosign signer + verifier so
the ext4 itself is integrity-protected. This ADR is the seam
between the two: the **provenance row** that names what ran and
where, plus the public API surface that lets a customer (or an
auditor) read it.

## Decision

Ship the **build provenance vertical slice** in this PR:

1. **`build_provenance` table** — one row per build, uniquely keyed
   on `build_id`. Fields: `buildkit_version`, `railpack_version`,
   `base_digest`, `source_sha256`, `source_url`, `plan`, `runner_digest`,
   `builder_node_id`, `started_at`, `finished_at`, `sbom_storage_key`.

2. **Populator** — `builderd` writes the row at the two `markSucceeded`
   sites (cache-hit path + fresh-build path). The write is
   best-effort (`ON CONFLICT (build_id) DO UPDATE` makes redelivery
   safe); a failed write logs at WARN and the build still succeeds
   (the `builds` row is authoritative for the customer-visible
   success/fail transition).

3. **Read surface** — `GET /v1/builds/{id}/provenance` in apid. The
   handler calls `Store.BuildProvenanceByBuildID` and renders a
   `BuildProvenanceResponse` DTO (pkg/api/dto.go). `faas build
   provenance <id>` surfaces the same row in the CLI.

4. **`sbom_storage_key` column** — present but not populated in this
   PR. The populator lands in Phase 3 alongside the cosign
   signer. The column exists now so Phase 3 is a zero-cost schema
   change.

5. **No new ADR for the pop. write path** — the pop. lands inside
   `Builderd.recordProvenance`, a private method called from the
   two existing `markSucceeded` sites. The PR diff is contained
   to `pkg/builderd/builderd.go` + `cmd/builderd/config.go` (new
   `BuilderNodeID` + `BaseRef` config fields).

### Why a separate table, not columns on `builds`

`builds` is a state-machine row: status, started_at, finished_at,
failure_class, log_path. Provenance is a separate concern (what
ran) from state (whether it ran). Conflating them bloats the row
that's read on every wake-failure redelivery (cache-hit rebuilds
the state row, not the provenance row), and forces every future
provenance field to be a `builds` schema migration. A separate
table also lets the read path index on `build_id` only — the
state row stays write-heavy and narrow.

### Why no signer in this PR

ADR-038 is the seam between Phase 1's schema half and Phase 3's
attestation. Splitting into three PRs (schema / populator /
verifier) is what makes the review ~10 minutes: each PR is one
vertical slice, and the Phase 3 verifier PR doesn't need to also
ship the schema. Phase 3 introduces `pkg/cosign/{signer,verifier}.go`,
fails loud on missing `/etc/faas/secrets/sign.key`, fails
`DeployFailed` with `error_code = "sig_invalid"` on bad sig, and
emits the SBOM populator that fills `build_provenance.sbom_storage_key`.

### Plan tiering

Provenance is captured for every plan (Free through Scale). The
populator cost is one INSERT per build — bounded by the
`builds_running_idx` partial index's row-count, not by plan
tier. The CLI command `faas build provenance` is unrestricted; the
apid route is gated by the same `BuildByID` auth the customer
already has.

### Provenance row contents (rationale per field)

- `buildkit_version` — `BuildKit` package version inside the
  builder microVM. Filled from `FAAS_BUILDERD_BUILDKIT_VERSION`
  config (default "0.31.1" until pinned by a future builder-base
  rebuild). Empty string when the build was a cache hit
  (buildkit didn't run).
- `railpack_version` — `Railpack` package version. Same shape as
  buildkit_version. Empty on cache hit.
- `base_digest` — sha256 of the base ext4 attached to the
  builder VM. Filled from `FAAS_BUILDER_BASE_REF` digest (the
  env var cmd/imaged already digest-pins). Empty on cache hit.
- `source_sha256` — sha256 of the customer's source tarball.
  Already cached by `b.cache.Lookup(srcHash, ...)`; the
  populator reads it from the cache-hit branch's input arg.
- `source_url` — copied from `deployment.source_url`
  (Tier 3 Phase 1 column, populated by githubd).
- `plan` — copied from `acct.Plan` at the claim site.
- `runner_digest` — sha256 of the function runner shim injected
  at build time (spec §4.9). Filled from the `FunctionRunnerPath`
  resolved in cmd/imaged. Empty for non-function deploys.
- `builder_node_id` — the builder microVM's compute_node name
  (default "default-local" on the one-box). Filled from
  `BuilderNodeID` config; cmd/builderd wires the value at boot.
- `started_at` / `finished_at` — copied from the `builds` row's
  matching columns.
- `sbom_storage_key` — populated by Phase 3's syft path. Empty
  string in this PR.

### Read surface — auth shape

`GET /v1/builds/{id}/provenance` is gated by the existing
`build:read` scope (ADR-034 rev2). The handler loads
`BuildByID`, then `BuildProvenanceByBuildID`. The DTO is
identical to the column shape with one extra: `id` (the
provenance row's PK, returned for log correlation). A 404 from
`BuildProvenanceByBuildID` returns `404 build_provenance_not_found`
when the build row exists but the provenance write failed
(defense in depth — a successful build with no provenance row is
the WARN-logged population failure path, surfaced as 404 rather
than 200 with an empty body).

## Consequences

Positive:
- Auditors can answer "what ran?" with one query against
  `build_provenance`. The customer's downstream SBOM scanner
  reads `sbom_storage_key` (Phase 3) without re-scanning the layer.
- The populator lands inside the existing `markSucceeded` paths —
  no new state machine, no new audit event kind, no new
  webhook surface. The diff is ~30 lines of Go.

Negative:
- One new table + one new partial index
  (`build_provenance_build_id_idx`). On a one-box the row count
  is bounded by `builds` lifetime (≤10⁵ rows at the §17 retention
  horizon). The index cost is negligible.
- A failed provenance INSERT logs at WARN and surfaces as 404 on
  GET — the customer-visible failure mode is "provenance missing
  for build X" rather than "build X failed". The build itself
  still succeeded; the pop. failure is observational metadata.
  Worth a follow-up ticket to surface this in the dashboard
  (out of scope for this PR).
- `SourceURL` + `CommitSHA` flows from `deployment` to
  `build_provenance` via the existing Phase 1 columns — no new
  data path, but a future "deploy with no upstream URL" leaves
  both fields empty in provenance (the customer's
  image/tarball/dockerfile deploy shape today).

Compatibility:
- All fields are nullable. Pre-existing `builds` rows from before
  this migration have no provenance row — `BuildProvenanceByBuildID`
  returns `ErrNotFound` and the apid handler renders 404. No data
  backfill (a Phase 4 follow-up could join `builds` ↔
  `deployment_log` and re-derive, but the cost/benefit is poor for
  builds > 30 days old).
- `ON CONFLICT (build_id) DO UPDATE` makes the populator
  idempotent. A redelivered build (LISTEN race) writes the same
  values; no audit-log noise.

## Open follow-ups (not blocking)

- **Phase 3** (in flight): cosign signer + verifier + SBOM
  populator. `pkg/cosign/{signer,verifier}.go`,
  `/etc/faas/secrets/sign.key`, `faas keys init/rotate/status`,
  fail-loud on missing key, DeployFailed with `error_code =
  "sig_invalid"` on bad sig. Will land as a separate PR with the
  same sprint scope.
- **Phase 4** (proposed): KMS-backed signing via cosign's
  `--key` flag. ADR-039 to land when the EX44 picks up a KMS
  endpoint; `FAAS_SIGN_KEY_KMS_URI` env var will switch the
  signer impl.
- **Provenance dashboard panel** — `provenance_writes_total{code}`
  counter, pre-instantiated from boot. Lands when the dashboard
  team picks up the request.

## Rejected alternatives

- **Sign every build's OCI image with Notary v2 in BuildKit.** Rejected:
  adds a second trust root (Notary + sigstore), and Notary v2 is not
  yet GA in buildkit 0.31.x. Cosign is the sigstore-native shape
  and matches the rest of the cloud-native ecosystem.
- **Per-tenant keypair (one key per account).** Rejected: operator
  overhead (100s of keys to rotate), no incremental security (an
  attacker who compromises imaged reads the active key regardless
  of which tenant's layer is being signed), and the verify side
  becomes a per-tenant key lookup that's redundant with the
  customer's own supply-chain signing.
- **Provenance on `builds` directly.** Rejected: bloats the
  state-machine row that's read on every wake-failure
  redelivery; conflates "what ran" with "whether it ran"; forces
  every future field to be a `builds` schema migration. The
  separate table indexes on `build_id` only.
- **Inline SLSA-style provenance embedding in the OCI image.**
  Rejected: the produced layer is an ext4, not an OCI image. The
  OCI tarball is the build VM's output; the ext4 is what
  cold-boot consumes. The two are different artifacts with
  different trust roots.
- **Inline populate during `markSucceeded` via a tx wrapper.** Rejected:
  the existing `markSucceeded` path is `UpdateBuildStatus + ops
  observe`, not transactional with anything. Wrapping in a tx
  would force every call site to handle tx errors. Best-effort +
  ON CONFLICT is the honest answer for a metadata table that
  doesn't gate customer success.