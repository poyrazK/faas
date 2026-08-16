# ADR-110 · Declarative split-box deployment manifest

- **Status:** **Accepted** (revised 2026-08-16) (PR-0 of the issue #911 PR cluster)
- **Date:** 2026-08-15
- **Decision:** Adopt a single, versioned, declarative deployment
  manifest as the source of truth for every host in a multi-box
  Gregale fleet. The manifest is a YAML document at
  `deploy/manifest/splitbox.yaml` (operator-supplied) plus a typed
  schema at `pkg/manifest/` (in tree). Every reader of the manifest
  — the validator (`gregalectl manifest validate`), the renderer
  (PR-2), the release bundle installer (PR-3), the doctor
  preflight (PR-4), and the metal harness (PR-6) — consumes the same
  `pkg/manifest` package. **One canonical validation path**. The
  legacy `deploy/controlplane/bootstrap.sh` is retired (PR-1) once
  the per-secret replacement (`gregalectl secrets init`, PR-X) and the
  replacement ansible roles land.
- **Why:** Issue #911 records that a 10+ hour live-debugging session
  on the GCP split-box deployment was caused by configuration drift
  between the boxes — the code assumed a multi-box install while the
  fleet was partially wired as a single-box install, and every
  operator-driven fix (TOML edits, hostname → VPC address, cgroup
  remount shim, file ownership, image refresh) had to be applied
  by hand. The codebase already has partial multi-box scaffolding
  (`pkg/role`, `pkg/pki.RolesForBox`, `pkg/wire.PGNodeVerifier`,
  `deploy/ansible/host_vars/faas-fsn-{1,2}.yml`) but the deployed
  machines weren't generated from it. The fix is not "more ansible
  roles" — it's inverting the dependency so that one declarative
  manifest drives every provisioning step, with the existing scripts
  becoming thin renderers that consume the manifest.
- **Consequences:**
  - **Schema (PR-0, this PR):** new `pkg/manifest/` package with a
    typed schema covering hostnames, roles, endpoints, overlay,
    DNS, PostgreSQL, release digests, storage roots, cgroup
    requirements, and PKI. Schema is SemVer (currently 1.0.0);
    major-version bumps are breaking (rename, mandatory field,
    tightened enum); minor + patch are backward-compatible.
  - **Canonical validation path (PR-0):** every reader of the
    manifest consumes `pkg/manifest.Manifest.Validate`. Issue #911's
    "completeness contract" explicitly requires this; the
    `cmd/gregalectl manifest validate` subcommand is the operator
    surface, and `make lint-manifest` is the CI gate. There is no
    "third-party" validator that disagrees with the canonical one.
  - **TOML table-placement catalog (PR-0):** the bug at
    `deploy/ansible/roles/vmmd_service/files/vmmd.toml.example`
    lines 33-40 (duplicated `tls_*_path` cluster declared inside
    `[compute_node]` — the canonical location is the top-level
    `tls_cert_path` / `tls_key_path` / `tls_ca_path` group) is
    the load-bearing failure mode
    issue #911 calls out. The schema's `pkg/manifest/toml_check.go`
    catalog is the source of truth for which key belongs to
    which TOML table. The renderer (PR-2) consumes the same
    catalog, so the renderer and the validator cannot drift.
  - **Migration slots reserved (PR-0):** `migrations/00266*.sql`
    and `migrations/00267*.sql` are reservation fences added in
    PR-0 per the team's slot-race posture (see MEMORY index
    entries on "migration gates collision" and "cross-PR slot
    precheck"). The bodies land in PR-3a (the `compute_nodes`
    release columns + the `release_bundles` table).
  - **Renderer (PR-2):** `gregalectl manifest render --role=…`
    consumes the manifest and emits `/etc/faas/*.toml`,
    systemd units, tmpfiles (including `/run/faas/stream`),
    cgroup v2 `subtree_control` (the load-bearing gap — only
    `deploy/lima/run-metal.sh:84` writes `subtree_control` today),
    and PKI leaves via `pkg/pki.RolesForBox()`. Idempotent
    (hash-match short-circuit); atomic publication via tmpfs `mv`.
  - **Doctor (PR-4):** `gregalectl doctor` is the operator-facing
    preflight that surfaces every drift the issue's
    "must report actionable failures for" list names. The doctor
    consumes the manifest schema and the rendered TOML catalog;
    the manifest drives the checks.
  - **Release bundle (PR-3):** `gregalectl release bundle --git-sha <sha>`
    produces a content-addressed tuple at
    `/opt/faas/releases/<id>/` containing the binaries, the
    rendered config hash, the migration version, the FC/kernel
    hashes, the per-host digests, and the manifest hash. The
    install path is idempotent + digest-verified.
  - **Bootstrap secrets (PR-X):** `deploy/controlplane/bootstrap.sh`
    is the only writer today for `deploy_ed25519` (the CD
    deploy-key), `host.age`, `session.key`, and the storage-box
    `rclone.conf` / `box-age-key` files. PR-X ships
    `gregalectl secrets init` (env-var → canonical paths) plus the
    parallel ansible roles. Once PR-X lands, PR-1 deletes
    bootstrap.sh.
  - **Metal harness (PR-6):** `deploy/lima/faas-metal-splitbox.yaml`
    is the two-role Lima fleet that runs the issue #911
    acceptance chain under `make metal-lima-splitbox`. The
    harness consumes the same manifest the renderer consumes,
    so the dev loop and the production deploy path cannot diverge.
  - **Issue #911 acceptance criteria mapping:**
    - "Fresh control-plane + compute-node pair provisions from
      empty machines." → PR-2 + PR-3 + PR-X.
    - "No manual host-file edits, TOML edits, direct SQL
      repairs, or ad-hoc binary copies." → PR-2 + PR-3a + PR-5.
    - "`doctor` passes on both boxes." → PR-4 + PR-6.
    - "Compute node remains active through multiple heartbeat
      cycles." → PR-5 (the `pkg/wire/pgverifier.go` receiver
      fix at the `Run` loop).
    - "mTLS handshakes without manual DB row insertion." →
      PR-3a + PR-2.
    - "CLI deploy + cold wake + HTTP 200." → PR-6.
    - "Same flow passes in local metal harness before fleet
      rollout." → PR-6 (the gate).

## Schema

Every required field is documented in `pkg/manifest/manifest.go`.
The loader refuses non-YAML files (TOML is explicitly rejected, per
the same convention as `pkg/gregalemanifest/manifest.go` — silent
ignoring would let customers think their manifest was applied).
The schema's TOML table-placement catalog is at
`pkg/manifest/toml_check.go`.

## Out of scope (explicit, v1.1+)

- **Vault integration for secrets** — PR-X's `gregalectl secrets init`
  accepts base64-encoded env vars. A Hashicorp Vault / AWS Secrets
  Manager integration is a v1.1 follow-up; the issue doesn't
  mention a vault.
- **Operator-facing manifest editor** — the v1 surface is a YAML
  file plus `gregalectl manifest validate`. A `gregalectl manifest
  edit` interactive surface is a v1.1 follow-up.
- **Schema auto-migration** — when a manifest's schema_version
  is older than the running binary's SchemaVersion, the
  validator flags the diff but does not auto-rewrite. A migration
  helper is a v1.1 follow-up.
- **Per-host overlay provider** — the manifest declares one
  provider for the whole fleet. Per-host overlay provider is a
  v1.1 follow-up.
- **Multi-region** — the manifest models a single region. A
  `region` field per host is a v1.1 follow-up (ADR-053 covers the
  per-node capacity signature, but the deployment manifest
  doesn't yet model cross-region).
- **Schema-evolution policy beyond the previous major** — when
  the schema bumps to v2.0.0, the previous major is supported
  for one release cycle. Longer-term deprecation policy is a
  v2 follow-up.

## Migration slot reservation

PR-0 reserves slots 00266 and 00267 with no-op `select 1;` bodies
per the `migrations/00056_reserve_slot.sql:1-50` pattern. The
bodies land in PR-3a:

- `00266_compute_nodes_release.sql` — `ALTER TABLE compute_nodes
  ADD COLUMN release_id text, manifest_hash text,
  host_certificate text, cert_fingerprint text, role text,
  generation int` (all nullable per the `00069` `region`/`zone`
  precedent).
- `00267_release_bundles.sql` — new `release_bundles` table
  recording `id`, `git_sha`, `manifest_hash`, `daemon_hashes
  jsonb`, `created_at timestamptz`, `applied_at timestamptz`.

## Cross-references

- Issue #911: "Make split-box deployment declarative and eliminate
  manual fleet configuration drift."
- ADR-025: cross-box mTLS — the original Tier 1 gate.
- ADR-052: PeerCN — the load-bearing chain + SAN + EKU + PeerCN
  primitive.
- ADR-056: `pkg/wire.PGNodeVerifier` — the per-CN handshake hook.
- ADR-070: Tier A7 edge split — the gatewayd-public /
  gatewayd-internal separation that completes the multi-box
  migration.
- ADR-083: active-passive HA — the next multi-box surface above
  the Gate-B primitives.
- ADR-092: Gate-B cross-box mTLS hardening — the operational
  scaffolding this ADR builds on top of.
- `pkg/role/role.go`: per-daemon box-role gate.
- `pkg/pki/pki.go`: `RolesForBox` partition.
- `pkg/wire/pgverifier.go`: the receiver fix in PR-5.
- `docs/runbooks/multi-host-rollout.md`: the operator narrative
  this ADR replaces.
- `docs/runbooks/manifest-renderer-cutover.md` (PR-7): the
  cutover runbook from legacy single-box to this world. The
  canonical operator narrative for first-time deployment.
- `docs/ops/gregalectl-operator-quickstart.md` (PR-7): the
  one-page operator reference; install `gregalectl`, bootstrap,
  write a manifest, validate, render, install, doctor.
- `deploy/lima/faas-metal-splitbox.yaml` (PR-7): the two-role
  Lima fleet that runs the issue #911 acceptance chain under
  `make metal-lima-splitbox`. The harness consumes the same
  manifest the renderer consumes, so the dev loop and the
  production deploy path cannot diverge.
