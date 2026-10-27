# Source deploys: GA hardening

Date: 2026-10-26
Status: Accepted
Amends: spec §17 G13 (stateless-only gate), ADR-101 (image-layer secret scan), ADR-20261023115741 (capability route ownership), issue #1182 (zero-config packing)
Related: ADR-115 (local tarball trust root), ADR-20261024100000 (Dockerfile HEALTHCHECK), ADR-20261019142544 and ADR-20261022185329 (Compose build options and secrets)

## Context

A GA review of `gregale deploy` from a local tree, tarball or Dockerfile found
behaviour that refused ordinary stateless apps, shipped the wrong bytes, or
left data behind:

1. The stateless gate refused any top-level `data/` or `db/` directory, so
   `db/migrations` (dbmate, golang-migrate, sqlc, Knex) could not deploy from
   the CLI. GitHub pushes, source-ref and project deploys skipped the gate, so
   the same repository deployed by one route and not another.
2. The server secret scan refused a deploy on any finding, including the
   `high_entropy` heuristic, with no override. An HTML Subresource Integrity
   attribute or a test-fixture token blocked a deploy. imaged failed deploys
   on the same heuristic findings in the built layer.
3. Accepted source archives and build-log directories stayed in
   `/var/spool/faas/builds` on the apid and builder hosts for good (production
   held 58 archives and 661 log directories from 2026-09-30 on 2026-10-26), and
   githubd kept every pushed commit's archive.
4. The CLI dropped `dist/`, `target/`, `vendor/` and other directories with no
   way to re-include them, refused committed symlinks, shipped submodules as
   empty directories and Git LFS files as pointers, and applied the plan's
   upload limit to uncompressed bytes.
5. `--mcp-task-migration-override` was silently dropped on the resumable
   upload path.
6. A single-app Dockerfile deploy could not select a Dockerfile or pass build
   arguments, a target or build secrets; only Compose could.
7. The `source-deploy` capability owned every deployment route, so its kill
   switch also hid image deploys and deployment status.
8. On a single box, a committed upload's `.part` (the build source) was
   deleted one hour after upload even while its build waited in the queue.

## Decision

1. **Stateless gate.** Refuse a database-store file under a top-level `data/`
   or `db/` directory of the selected source root: SQLite, DuckDB, Access,
   Redis, InnoDB/MyISAM and WiredTiger files by name, PostgreSQL and MySQL data
   directory markers, and any file with the SQLite signature
   (`pkg/statelessshape`). Other files there are source. The Dockerfile
   `VOLUME`/`mkfs`/`mount` checks are unchanged. The gate runs on every source
   ingress: multipart, resumable upload, legacy source tarball, source-ref,
   GitHub push and each project workload. The CLI doctor applies the same rule
   and no longer flags `var/` or an empty directory.
2. **Secret scan.** Only High findings refuse source: the provider credential
   formats, private keys and deploy tokens (`secretscan.Blocking`). Medium
   findings (`high_entropy`, `stripe_test`) are advisory: imaged still records
   every finding on the deployment (`gregale deployment ID --show-secret-scan`)
   but fails the deploy only on a High one. A Subresource Integrity digest
   (`sha256-`/`sha384-`/`sha512-` of the exact digest length) is never an
   entropy finding in any file. A line containing `gregale:allow-secret` is
   not scanned, the explicit escape hatch for fixtures and documented example
   keys. GitHub pushes now get the server scan too.
3. **Spool retention.** A sweeper (`pkg/sourcespool`) runs hourly in apid,
   builderd and githubd. It removes source archives, download temps, project
   workload archives and build-log directories last written more than
   `SourceSpoolRetention` (24 h, matching builderd's `source_max_age`) ago,
   never a file named by a deployment that is pending, building, imaging or
   snapshotting, and nothing when that list cannot be read. It only touches
   names it owns. The upload reaper keeps a committed `.part` for the same
   24 h and while its deployment is in flight; cancelled and expired parts keep
   the 1 h grace. `deploys retry` therefore works for 24 h on every install.
4. **Packing.** Defaults are overridable: a later `.gregaleignore` line such
   as `!dist/` re-includes a default-excluded directory, `.git` excepted. A Go
   `vendor/` with `vendor/modules.txt` ships. A Dockerfile build (a root
   Dockerfile or a `build:` section) keeps build-output directories
   (`dist`, `.next`, `target`, `coverage`, `.cache`) and applies
   `.dockerignore`, root-anchored as Docker reads it. An in-tree symlink is
   packed as the file or directory it points to; one that leaves the source
   tree, dangles or contains itself is an error naming the fix. The committed
   HEAD archive is normalized the same way before anything reads it:
   initialized submodules are merged at their recorded commits, Git LFS
   pointers are replaced from the local LFS store, and what cannot be resolved
   locally is a warning naming the fix. The CLI enforces the server's limits:
   the packed archive against the plan's upload limit, stopped as it is
   written, and the expanded source against `SourceArchiveMaxExpandedBytes`.
5. **MCP override.** A deploy with `--mcp-task-migration-override` uses the
   multipart upload, which carries it.
6. **`build:` in gregale.yaml.** `dockerfile`, `target`, `args` and
   `secrets` select and parameterize a Dockerfile build for a single-app
   source deploy, validated against the Compose limits. apid checks the
   selected Dockerfile exists and passes the stateless checks, and an explicit
   Dockerfile path selects the Docker pipeline whatever the deployment's
   provenance kind. A project app whose Compose file already declares a build
   refuses a second declaration in gregale.yaml.
7. **Capability routes.** Deployment read, status and control routes belong
   to the core `apps` capability. `source-deploy` owns the upload routes, and
   `POST /v1/apps/{slug}/deployments` is decided by its body: multipart by
   `source-deploy`, an image deployment by `container-deployments`.
8. **Evidence.** The `source-deploy` acceptance test is
   `TestSourceDeployWakeMetal` (multipart and resumable upload through build,
   live and wake). `TestDockerfileHealthcheckMetal` qualifies
   ADR-20261024100000 on a source-built image.

   The n2 (nested KVM) runs found two platform failures outside source
   deploys, both fixed with this work:
   - A new app's first request right after go-live got 429
     `app_concurrency_reached` (observed 0). A gateway placement read during
     the deployment smoke left out the snapshotting candidate, so the gateway
     quarantined the smoke instance it had admitted, and schedd counted that
     instance against `max_concurrency`. Placement reads now report
     snapshotting candidates as not live (kept, never discovered), and a wake
     answered at capacity serves a replica its rehydrate finds.
   - After `POST /v1/apps/{slug}/park`, a request-woken instance was
     destroyed by the reaper's must-park sweep, including a boot still in its
     restore or cold-boot fallback (503). Request wakes now claim the parked
     to active lifecycle (ADR-343 amendment 1).

## Consequences

- Repositories with `db/migrations`, schemas or seed data deploy from every
  route; a repository carrying a database file is refused from every route,
  including GitHub pushes that used to pass.
- Heuristic secret findings no longer block deploys; they stay visible on the
  deployment. Teams that want them blocking keep `--secret-scan=strict`.
- Spool disk use is bounded by roughly a day of deploys per host. A retry or
  runtime-upgrade rebuild of a source older than 24 h returns
  `Retry source unavailable`, as it already did for the object-store copy.
- Customer docs list what is uploaded, the limits, the scan and stateless
  rules, the `build:` section and the retry window.
- Rejected: dropping the directory-name rule without a replacement (loses the
  "shipped a SQLite file" signal); an allowlist file for the secret scan (a
  repository-wide bypass; the per-line marker keeps each exception
  reviewable); routing `--mcp-task-migration-override` through the upload
  session (needs an API and three generated SDKs for one flag);
  dereferencing symlinks on the server (the CLI is where the tree is known).
