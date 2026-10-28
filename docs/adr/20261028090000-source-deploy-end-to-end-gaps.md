# ADR 20261028090000 · Source deploys: the end-to-end gaps

- **Status:** accepted
- **Date:** 2026-10-28
- **Amends:** ADR-1500 (slice 4: the shared runtime is on by default),
  ADR-20261026152731 (source deploy GA hardening), ADR-791 (build timeout copy)
- **Related:** ADR-003 (builder microVMs), ADR-005, ADR-198 (revisions),
  spec §4.6, §6

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

An end-to-end review of source deploys after ADR-20261026152731 followed a
push and a tarball from the edge to a serving app and found six gaps:

1. **Builder disk.** builderd refuses a build drive unless the drive
   directory's filesystem has `BuildDriveMinFreeBytes` (16 GiB working set
   plus a 4 GiB reserve). imaged evicts snapshots only above 90 % of
   `/srv/fc`. On a 107 GB `/srv/fc` every build failed between about 78.5 %
   and 90 % full. Production recorded 63 such failures in 30 days, each an
   uncoded `insufficient host free space for build drive` with no retry,
   while the developer dependency cache held 13 GB in the same directory.
2. **Free plan.** The shared runner base (ADR-1500) was off by default, so a
   Node source app's layer carried its toolchain (about 369 MB on
   `base-minimal`) and failed Free's 256 MB layer cap with
   `app_layer_too_large`.
3. **Links.** Every source path but the CLI refused tar link entries: the
   GitHub fetcher and apid's scan extraction rejected all of them, and the
   shape check rejected any `..` in a link target. A repository with
   `CLAUDE.md -> AGENTS.md` or `packages/api/LICENSE -> ../../LICENSE` could
   not deploy from a push (a 500 that GitHub redelivers forever) or from an
   uploaded tarball (`entry type 50 not allowed`).
4. **Superseded builds.** A latest-revision deployment that a newer revision
   already outpaced still built to completion before the promotion fence
   superseded it. A burst of pushes built every revision in turn on the one
   guaranteed builder slot.
5. **Uncoded failures.** A build failure that was not OOM or a timeout
   reached the customer as an uncoded deployment error, with no hint, fix or
   log pointer.
6. **Organization pushes.** The GitHub bridge admitted an organization app's
   push against the namespace account (holds, billing status, plan, deploy
   rate) instead of the organization that owns the app, unlike the API path.

## Decision

1. **A full builder disk queues the build.** Before refusing, builderd
   reclaims the developer dependency cache, oldest entries first, until the
   drive's free space suffices. If it still does not, the build returns to
   the queue (`ErrBuildDiskFull`, requeue reason `build_disk_full`) after
   `BuildDiskCapacityRetryDelay` (30 s, `pkg/api/limits.go`) instead of
   failing. The requeue keeps `builds.enqueued_at`, so the queue watchdog
   still ends a build that never finds room (`deployment_queue_timeout`).
   imaged's 90 % eviction threshold is unchanged: it is spec-level snapshot
   policy and needs its own decision.
2. **The shared runtime is on by default, on glibc runner bases only.**
   `FAAS_SOURCE_APP_SHARED_RUNTIME` becomes an opt-out (`0`). builderd picks
   the runner base before the build, and guest-init's eligibility verdict
   comes after it, so an ineligible app ships its own toolchain above the
   runner base. Railpack's mise Node and any native addon are glibc builds.
   runner-node22 is Alpine (musl), and the first acceptance run failed there:
   an app with an npm lifecycle hook crashed with
   `node: cannot execute: required file not found`. Node 22 apps
   therefore keep their own base (`sharedruntime.MuslRunner`). Undeclared,
   lower-bound and Node 24 declarations use the Debian runner-node24. A Node
   22 app on Free still needs Node 24 to fit the 256 MB cap.
   `TestSharedRuntimeRollbackMetal` is the ADR-1500 acceptance. One app has
   three deployments: a Node 22 app on its own base, an ineligible Node 24
   app on the runner base, and an eligible `/app`-only Node 24 app. All three
   serve. The own-base and shared deployments each serve after a rollback
   onto them, from a cold boot and from a snapshot restore. The Free plan case
   of `TestE2E_Streaming_Metal_PlanMatrix` (fixture now Node 24) covers the
   256 MB cap.
3. **In-tree links deploy.** A symlink's target resolves relative to the
   link's directory, a hard link's target is an archive path
   (`tarball.LinkTarget`); only absolute or escaping targets are refused.
   - The shape check accepts in-tree links.
   - apid's scan extraction skips them: the target is scanned as its own
     entry and the builder guest extracts the link.
   - The GitHub fetcher replaces each in-tree link with a copy of its
     target, as `gregale deploy` does locally. Copies count against the
     entry, per-file and total byte caps; dangling links, cycles and links
     to an ancestor are left out, and so is a link that leaves the tree,
     which would dangle in any other checkout.
   - No path ever creates a link on the host.
4. **An outpaced build stops at its claim.** Right after loading the claimed
   build's deployment, builderd applies the promotion fence's own test
   (`HasNewerDeploymentRevision`). For a latest-revision deployment that is
   pending, building, or already superseded while pending, a newer revision
   in the same scope cancels the build and supersedes the deployment in one
   transaction, fenced on the claim (`OutpacedBuildStore`). Environment-
   managed workloads keep their graph activation's ownership.
5. **Every build failure has a code.** A user error is `build_failed`; an
   infrastructure failure is `build_infrastructure_failed`; OOM and timeout
   keep their stage codes. Each has a whycopy entry, and the timeout copy
   says 15 minutes (ADR-791).
6. **Organization pushes are admitted against the organization.** The
   bridge resolves the app's resource account for holds, billing status,
   plan limits, source policy and the deploy rate key. Email verification,
   freezes and activity stay on the namespace account, as on the API path.

## Consequences

- A builder node short of disk delays builds instead of failing them, and
  gives back cache space first. A node that stays full for six hours fails
  its queued builds with the queue timeout code.
- Node source apps on every plan, Free included, deploy with an `/app`-only
  layer when eligible and not pinned to Node 22. Apps that declare system
  packages, npm lifecycle hooks or other ineligible traits keep their
  toolchain in the layer.
- Pushes and tarballs with ordinary repository links deploy. A link out of
  the repository builds as if its target were missing.
- A burst of pushes builds only the newest revision once its turn comes.

## Validation

- `TestCheckBuildDriveCapacityReclaimsDependencyCacheFirst` (linux),
  `TestProcessOne_BuildDiskFullRequeuesTheBuild`,
  `TestReclaimDependencyCachesFreesOldestFirst`.
- `TestSharedRuntimeRollbackMetal`, `TestSourceDeployWakeMetal` and
  `TestE2E_Streaming_Metal_PlanMatrix` on the n2 metal host;
  `TestSharedSourceAppRuntime`, `TestMuslRunner`.
- `TestLinkTarget`, `TestExtractTarGz_InTreeLinksAreSkipped`,
  `TestExtractStreamCopiesInTreeLinks`,
  `TestExtractStreamLinkCopiesCountAgainstTheCaps`,
  `TestFetch_EscapingLinkIsLeftOut`.
- `TestMemSupersedeOutpacedBuild`, `TestPgSupersedeOutpacedBuild`,
  `TestProcessOne_SkipsBuildOutpacedByNewerDeployment`.
- `TestBuildFailureCodeCoversEveryClass`, `TestEveryCodeHasWhycopyEntry`.
- `TestEnqueueBuild_OrganizationAppUsesResourceAccount`.
