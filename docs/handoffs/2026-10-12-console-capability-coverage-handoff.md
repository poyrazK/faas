# Console capability coverage handoff — 2026-10-12

## Delivery status

The user freed storage and authorized continuing K–M inline. K implementation has resumed and is now implemented locally; publication and final checks are recorded below. Nothing was merged, deployed, or tested against customer/provider traffic. Earlier K/runtime sections below are retained as historical evidence and are superseded by this checkpoint.

### Resume checkpoint — K implementation

**Latest publication checkpoint:** K console is verified and pushed/open in [faas-web#173](https://github.com/poyrazK/faas-web/pull/173), head `624b248b6a97ce88a691af195ca345bece656524`, base J. Full frontend gate passes247 files/1,755 tests, type/lint/format and build31 routes; four existing lint warnings remain. Backend API is pushed/open in [faas#4632](https://github.com/poyrazK/faas/pull/4632), dependent on draft store#4614. Its latest source moves recovery to `pkg/apid/apprecovery`; local637c1193b8 corresponds to publisheded9784e633fb1652990be11aa98032a8ba10e755, exact tree08d463d6e4ea544daca39dffa4df3ab8617bc8a8. Domain3 tests, focused routed/security/existing restore tests, vet and size guard pass. Server155960 lines is within the unchanged official main15a8415 baseline155967; auth/route/lifecycle adapters preserve callback order.

**Integration limits:** Store#4614 is MERGEABLE but CI BLOCKED: codegen gate saw concurrently merged servergrowth155967→155974(+7), and `TestProductionRouteMonitorPostgresEscalatesOnceForNewSignal` failed on PostgreSQL. The post-extraction full server run also hit unchanged `TestListBuilds_OK_Pagination` and `TestObjectMultipartConditionalRecovery` failures. Earlier full API/server passes remain evidence but do not make these later full runs green. These failures are preserved in the ledger/logs and PRbody; do not hide them, merge, or close issues manually.

**Next product work:** L2 is now in progress inline on independent frontendbranch `feat/console-mcp-guidance` based on J. Plan9868155 and safe metadata/CLIclientbb60b5a are committed; client9 and UI10 tests/typecheck pass. MCP Connect integration/mock registry is uncommitted and under verification. Backend docs/CLI contract pin15a8415ea48821b71e8551cb8a19ca0d0f61dd5e. The L2 ledger is `.superpowers/sdd/2026-10-12-console-mcp-guidance/progress.md`; resume at its first incomplete verification task. L1 safe companion read projection is tracked in [faas#4633](https://github.com/poyrazK/faas/issues/4633); no L1 product code yet. M remains unimplemented and unsupported mutations gated. Own PostgreSQL55439 and Kmock3049 are stopped; unrelated work/processes remain untouched. The earlier checkpoint bullets below are chronological history.

- Fresh immutable main pins: frontend `b583966538e6097b3270c3c010c353e65115f554`; backend `35a53399ad554ba7ff39dd07eb707e683294ca0e`. Relevant backend contracts remain unchanged from `e9527df9`. Dirty main worktrees remain untouched.
- Backend store remains draft #4614. Separate API branch `feat/deleted-app-recovery-api` is based on published store/handoff `5ebdf37`. Local API commit `68194b1b0d18396c667f0d383f8957ed3af5b189` has the exact same tree `b3fad628cd96a11be0e0d7ab2a2f4f1e7b59d170` as published API commit `57262165306f7377dc223441c933b9d3445dfb61`.
- API implements owned bounded GET `/v1/deleted-apps`, exact-ID GET, and dedicated POST `/v1/deleted-apps/{id}/restore`, strict account-bound cursor/query/body validation, lifecycle blockers, exact-ID/slug-collision fencing, Go SDK and mirrored OpenAPI. Existing deploy-write/MFA/origin/quota/purge protections remain. Dedicated restore has no idempotent replay or legacy fallback.
- Frontend branch `feat/console-deleted-app-recovery`, base J: atomic commits `cf4e242` contract/read client, `e6934da` durable reviewed restore, `fbc51f3` Recently deleted inventory/review/reconciliation/route/mock. Follow-up regression/format cleanup is pending commit. This is a real journey, not a placeholder.
- Frontend reads whitelist account-owned metadata before caching. Review and submit each refresh exact identity/context/deadline. Nonsecret session-scoped attempt identity precedes POST; automatic retries are disabled. Reload/lost-response recovery uses explicit reads and exact ID, distinguishes accepted intent, observed restored metadata, and unverified execution health.
- Current backend store tests: MemStore2 + actual PostgreSQL1 GREEN with no skips, full migrations/new index. Focused API15 top-level plus quota/capacity subtests GREEN; SDK/coverage/AST/OpenAPI/SQLc checks pass. Full API and server packages pass. Full state package had one unrelated `TestObjectEncryptionMultipartJournalMem` timing failure; the unchanged test passed in isolation afterward. Do not call that full state run green. Build/vet final status remains pending at this checkpoint.
- Frontend focused7 files/27 tests + typecheck pass; existing real HTTP mock regression plus UI45 tests pass after correcting a deployment-free restored fixture to `undeployed`. Full check/build final status remains pending.
- Mobile Edge mock walkthrough passes: bounded 25-row pagination, Back/Forward/reload, keyboard/Escape, foreign account cursor rejection, expiry/purge/null metadata blockers, quota failure, accepted response loss then reload/read-only exact-ID reconciliation, denied reads, delayed navigation. Zero page errors; only two deliberate fixture restore POSTs. No live app was deleted to test.
- One fresh GPT-6.1 Sol/High final reviewer found no verified Important/Critical blockers. No implementer fan-out or repeated A–J/L3 review.
- C: now has about18GB free; native Linux `GOTMPDIR=/home/bahadir/.cache/gregale-k-native-tmp` avoids NTFS permission/time truncation problems. Task Go cache remains on D:. Tests use UTC and GOMAXPROCS4. Own private PostgreSQL is active on55439 during qualification; stop only that fixture afterward.
- First remaining scope after K publication: L1 companions safe read projection; then L2 MCP guidance and M precisely limited reads. L1/L2/M have no product implementation yet. Unsupported M clone/enforce/traffic-moving controls remain gated; no staging qualification authority was inferred.

Durable current ledger: frontend `.superpowers/sdd/2026-10-11-console-deleted-app-recovery/progress.md`. Its latest checkpoint and logs override the historical stop record below.

- A–J are implemented in the existing open frontend stack. Their implementation/reviews should not be repeated.
- L3 OpenAPI comparison is implemented, verified locally, pushed, and open in [faas-web#169](https://github.com/poyrazK/faas-web/pull/169).
- K has a published store foundation in draft [faas#4614](https://github.com/poyrazK/faas/pull/4614), a published API branch, and locally implemented console recovery. K release checks/PR publication remain pending at this checkpoint.
- L1 companions, L2 MCP, and M remain incomplete. Unsupported M operations remain gated.
- The backend store draft must not close [faas#4423](https://github.com/poyrazK/faas/issues/4423) or [faas-web#154](https://github.com/poyrazK/faas-web/issues/154). Neither issue's full requirements are implemented.

Authoritative specification: frontend `docs/superpowers/plans/2026-10-08-console-capability-coverage.md`. Tracking parent: [faas-web#134](https://github.com/poyrazK/faas-web/issues/134).

## Open PR and issue snapshot

Read from GitHub on 2026-10-12. Mergeability is against each PR's current base, not a guarantee about future rebases or staging health. All listed PRs are open and unmerged.

| Scope | PR | Base | Issue closure text | GitHub snapshot |
| --- | --- | --- | --- | --- |
| A discovery/storage guards | [#144](https://github.com/poyrazK/faas-web/pull/144) | main | Closes #136 | MERGEABLE / CLEAN; checks successful |
| B HTTP OCI deployment | [#145](https://github.com/poyrazK/faas-web/pull/145) | A | Closes #137 | MERGEABLE / CLEAN; checks successful |
| C project inventory/import | [#146](https://github.com/poyrazK/faas-web/pull/146) | B | Closes #138 | MERGEABLE / CLEAN; checks successful |
| D environment inspection | [#147](https://github.com/poyrazK/faas-web/pull/147) | C | Closes #139 | MERGEABLE / CLEAN; checks successful |
| E policy convergence | [#148](https://github.com/poyrazK/faas-web/pull/148) | D | Closes #140 | MERGEABLE / CLEAN; checks successful |
| F Stripe inbound webhooks | [#159](https://github.com/poyrazK/faas-web/pull/159) | E | Closes #149 | MERGEABLE / CLEAN; checks successful |
| G instrumented Issues | [#160](https://github.com/poyrazK/faas-web/pull/160) | F | Closes #150 | MERGEABLE / CLEAN; checks successful |
| H internal services | [#162](https://github.com/poyrazK/faas-web/pull/162) | G | Closes #151 | MERGEABLE / CLEAN; checks successful |
| I-a queue consumers | [#163](https://github.com/poyrazK/faas-web/pull/163) | H | Part of #152 | MERGEABLE / CLEAN; checks successful |
| I-b OCI workers | [#164](https://github.com/poyrazK/faas-web/pull/164) | I-a | Part of #152 | MERGEABLE / CLEAN; checks successful |
| I-c stage desired bindings | [#165](https://github.com/poyrazK/faas-web/pull/165) | I-b | Closes #152 together with #163/#164 | MERGEABLE / CLEAN; checks successful |
| J cache rules/targeted purge | [#166](https://github.com/poyrazK/faas-web/pull/166) | I-c | Closes #153 | MERGEABLE / CLEAN; checks successful |
| L3 OpenAPI comparison | [#169](https://github.com/poyrazK/faas-web/pull/169) | J | Closes #157 | MERGEABLE / UNSTABLE; check queued, Vercel successful |
| K backend store foundation (draft) | [faas#4614](https://github.com/poyrazK/faas/pull/4614) | backend main | Related to #4423/#154; closes neither | MERGEABLE / BLOCKED; draft and checks queued |

The closure statements are present in the PR bodies. Stacked PR closure takes effect only when integrated into the default branch; do not manually close issues now. I requires all three PRs. Parent #134 remains open because K, L1, L2, and M remain outstanding. Backend [#4390](https://github.com/poyrazK/faas/issues/4390) still tracks the complete recorded PR-root index; D only has app-backed candidates plus selected-root inspection. Backend [#4421](https://github.com/poyrazK/faas/issues/4421) tracks omitted cache action OpenAPI unions; J corrects its vendored frontend slice.

## Immutable pins and isolated workspaces

| Item | Identity |
| --- | --- |
| Frontend remote main, last checked | `c0d8b7f6f5d5c4c93629e25aa5a7be2eac25d1c0` |
| J dependency | `26e6234aa3d3d40281529684fc8cba86421d208c`, `feat/console-cache-rules-purge` |
| L3 pushed head | `c010a217e008d1807f0122781d51306c348c7382`, `feat/console-openapi-policy-preview` |
| K frontend | `feat/console-deleted-app-recovery`, based on J; plan-only commits `3c5d26a`, `13f0eb8`, `c7580e0` |
| K backend local store commit | `43580bdf834d5ae1ddaebeecd91c3f71086d9a3f` |
| K backend published store commit | `2d897871f16700f3ba1e10547ba5641e57bafd24`, `feat/account-deleted-app-inventory`, draft #4614 |
| K backend current base | `e9527df9e5eb03b1963dab424498afd76ad5beeb` |
| Backend remote main, final read-only check | `3c8e79bb5262b8595c624a9f7f5e7347f5a5a3b1` (advanced again; not adopted or qualified) |
| Backend base for recorded store GREEN | `705298f5ec8b1933f94d4e16a0a0df5c578914a3` |

Frontend worktree: `/mnt/d/gregale/.codex-worktrees/faas-web/landing-copy` (currently K branch). Backend worktree: `/mnt/d/gregale/.codex-worktrees/faas/account-deleted-app-inventory`.

Both main checkouts remain dirty with unrelated user work and were never reset, stashed, copied, or modified. Read backend contracts using immutable Git objects. The newer backend moved server handlers/tests from `cmd/apid` to `pkg/apid/apidserver`; recheck remotes and applicable instructions before resuming. Preserve all existing A–J PR heads.

Local Git/gh credentials identify `jackthepunished` and have read-only access to the backend, so a direct push returned 403. The repository owner's connected GitHub account published the nine exact store blobs and a new commit. Local and published store trees both equal `4068d56ec95f49cec76393a892eacc59e771995b`; only commit metadata differs. A separate documentation commit adds this handoff. Preserve the original local history as `feat/account-deleted-app-inventory-local-snapshot` and align the active branch with the published history by fetching/switching, without reset or force-push. Backend main advanced again while opening the draft (PR initial base `fae712e087b62917db5a75da216ad077fb0dcf9f`); its newer changes are not qualified by the recorded store tests.

## L3 — what was delivered

Atomic commits on `feat/console-openapi-policy-preview`:

1. `b572494` — bound the implementation plan.
2. `ad9ae9a` — sanitized OpenAPI policy-preview contract/client.
3. `2c3faff` — route comparison UI and mock behavior.
4. `c010a21` — all-method policy and preview invalidation fixes.

The actual GET preview is account/app scoped and abortable. Cached results whitelist matching metadata and drop opaque rule actions/suggestions. Both existing OpenAPI entry points show matched, declared-only, observed-only routes; enabled/disabled policy matches; collector, inventory, and cap evidence; partial/unavailable/healthy empty observation distinctions; filtering and 20-row paging. Existing import/dry-run/discovery flows remain available. Import/delete and edge-rule changes refresh the derived preview. Backend `match_methods: null` means all methods and is normalized safely. Coverage does not prove policy correctness or collector health.

Recorded verification at pushed head:

- Client/UI/mock behavioral RED before implementation.
- One fresh final reviewer, user-selected GPT-6.1 Sol / High. Null all-method rendering and stale previews fixed in one pass; six regression assertions observed RED then GREEN.
- Focused five files / 24 tests and typecheck passed.
- `npm run check && npm run build`: exit 0; 244 files / 1,745 tests, type/lint/format passed; production build generated 31 routes. Four preexisting lint warnings remain.
- Windows Edge mobile mock walkthrough: direct reload/history, keyboard, long lists/filter, partial retained inventory, permission failure, delayed app switch, actual null policy, secret action field absent; zero preview writes/page errors.
- No collector/provider/staging qualification claimed. Own mock server stopped.

Plan and ignored ledger/log/browser evidence: frontend `docs/superpowers/plans/2026-10-11-console-openapi-policy-preview.md` on the L3 branch and `.superpowers/sdd/2026-10-11-console-openapi-policy-preview/` in the existing worktree. Child [#157](https://github.com/poyrazK/faas-web/issues/157) and parent #134 are updated. CI check was still queued in the final snapshot; do not claim every remote gate green.

## K — store foundation delivered in draft

Nine source files, 425 additions in store commit `43580bdf834d5ae1ddaebeecd91c3f71086d9a3f`:

- Narrow optional `AccountDeletedAppsStore`, without expanding the shared Store interface.
- MemStore and PgStore list/detail implementations that apply account and deleted-status predicates before limiting.
- Bounded keyset pagination (1–51 rows), ordered by `coalesce(deleted_at, created_at)` and ID; stable ties and deleted/restored cursor-row behavior.
- Narrow metadata: ID, slug, nullable authoritative deletion/deadline, internal sort time, purge-claim presence. Global sweeper `ListDeletedApps` is unchanged.
- SQLc queries/generated output, partial account/deletion tuple index migration, actual generated schema index.

Recorded tests: missing MemStore methods observed RED; two MemStore tests and one actual PostgreSQL fixture test GREEN, no skips, including full fixture migrations/new index. Coverage includes account isolation, active-app exclusion, ties, missing legacy metadata, purge claim, pagination bounds, and cursor rows restored between reads. SQLc v1.31.1 regeneration and diff checks were completed.

**Qualification limit:** GREEN was captured on base `705298f5` before a clean rebase onto `e9527df9`; full post-rebase backend gates and a fresh final backend review were not run. API compilation was interrupted by resource pressure. This draft is a store foundation for review, not completed recovery functionality.

### Prepared work, not shipped

- Frontend K plan: `docs/superpowers/plans/2026-10-11-console-deleted-app-recovery.md`, plan-only commits listed above.
- Backend untracked test draft: `pkg/apid/apidserver/handlers_deleted_apps_test.go`. It is excluded from the PR. Tests cover proposed list/detail, cursor/account ownership, eligibility, permissions/MFA, valid slug `deleted`, legacy identity fence, and dedicated restore-by-ID. API behavioral RED was **not** observed; no API product implementation exists.
- Frontend untracked test draft: `src/lib/api/restore-app.test.tsx`, retained in the frontend worktree. Three initial RED assertions observed the old hook's legacy route/missing invalidation. Added explicit no-retry assertions were not rerun. Existing `useRestoreApp` is unchanged; no K frontend product code exists.
- Chronological ledger (latest entries override earlier failed attempts): frontend `.superpowers/sdd/2026-10-11-console-deleted-app-recovery/progress.md`. Own backend logs were moved into its ignored `backend-logs/` directory.

### Qualified contract direction for the next implementer

1. Use `GET /v1/deleted-apps`, `GET /v1/deleted-apps/{id}`, and `POST /v1/deleted-apps/{id}/restore`. `/v1/apps/deleted` would shadow the valid app slug `deleted`.
2. Console restore uses the dedicated ID route, with **no fallback to legacy slug POST**. Old servers ignore legacy bodies, so an optional `expected_app_id` alone cannot protect mixed-version fleets. The new route fails closed on old servers. Optional legacy fencing can separately preserve bodyless compatibility.
3. Read the owned narrow record, resolve slug, then recheck exact app ID and restore that ID; never restore a replacement app that reused the slug. Reject unexpected body intent such as `dry_run`.
4. Use a bounded, strict, account-bound opaque timestamp/UUID cursor. Proposed summary: `id`, `slug`, `deleted_at`, `delete_grace_until`, `restore_eligible`, `restore_blocker`, `evaluated_at`. Lifecycle blockers: `grace_expired`, `purge_started`, `metadata_unavailable`. Eligibility does not reserve quota/capacity.
5. Preserve deploy-write/MFA/origin, deadline/purge/quota/collision checks. Never use a global tombstone list in the console or infer deadlines solely from browser time.
6. No automatic POST retry. Reconcile ambiguous restore against exact-ID metadata using durable nonsecret pending identity. Distinguish accepted restore intent, observed metadata, and execution health.
7. Invalidate deleted-app, app, project, and deployment inventories after accepted restore. Frontend customer reads/writes wait for the qualified REST/SDK contract.

First resume point: inspect the newer backend main and prepared tests, run a bounded API behavioral RED harness, implement the API/SDK in a separate atomic commit, then complete required backend gates/review and the console journey. Do not restart at A–J or L3.

## L1 — companions remains pending

[faas-web#155](https://github.com/poyrazK/faas-web/issues/155). At backend `e9527df9`, companion/sidecar fields appear in create/upload DTOs but no deployment GET/AppManifest read projection was found. A working declared-companion summary requires a separate bounded backend metadata contract before frontend implementation.

Expose safe declared metadata (name/image/type/resources/dependencies/environment **key names** only). Never return environment secret values, sealed ciphertext, or arbitrary commands. Separate declared configuration from returned health; absent telemetry is Unverified. Provide manifest/CLI handoff. An unavailable preset does not imply every custom helper is unsupported. No L1 product code or new bounded implementation plan was written.

## L2 — MCP remains pending

[faas-web#156](https://github.com/poyrazK/faas-web/issues/156). Verified capability key at `e9527df9`: `mcp-hosting`, Hobby+, Preview. Use capability plus streaming availability; enabled does not prove protocol verification.

Guidance must follow `docs/mcp.md`: stateless Streamable HTTP, optional legacy transport, CLI `mcp doctor/config/tools/resources/prompts`. Doctor/catalog are CLI operations; no browser probes or tool execution. Client bearer tokens belong in environment variables (`--token-env MCP_TOKEN`); control-plane credentials are separate. Connection JSON contains no credentials; client shape may need adaptation. Current frontend docs manifest does not publish `/docs/mcp`; do not invent that link. Browser diagnostics require separate auth/origin/SSRF qualification. No L2 product code or new bounded implementation plan was written.

## M — incomplete and deliberately gated

[faas-web#158](https://github.com/poyrazK/faas-web/issues/158). Confirmed at `e9527df9`:

- `fullProjectEnvironmentCloneUnavailable()` deliberately returns conflict: complete resource coverage, coordinated checkpoint, fresh managed bindings, and isolated policies remain unavailable.
- Customer GitOps enforce returns `409 environment_git_enforcement_unavailable`. Production qualification polling, complete graph activation, and continuous enforcement are not enabled by this preview. Dedicated Linux x86_64 KVM test-metal/leakcheck acceptance remains a prerequisite.

Do not enable unsupported clone/enforce controls, change operator rollout flags, send traffic, or mutate production. Destructive/traffic-moving controls need separately authorized isolated staging qualification.

A precisely scoped read-only readiness/blocker/qualification/status journey may be implemented later. Current contract routes include clone detail, environment `gitops`, `gitops/adoption-preview`, `promotion-preview`, `promotions`, and selected `promotions/{promotion}`. Inspect exact contracts before writing clients. Preserve source revision, target generation/hashes, qualification/approval identities, and logical operation identity. Stale review requires a new preview; ambiguous operations reconcile durable receipts, never restart/blind rollback. Limited reads do not complete issue #158. No M product code or new bounded implementation plan was written.

## Runtime blocker and safe verification resume

WSL repeatedly failed before command launch or during Go compilation with filesystem I/O errors and unrequested restarts. Concrete pressure: C: dropped below 1 GB; Windows pagefile allocation grew to about 5 GB while API compilation reached about 8.3 GB RSS. `GOMEMLIMIT=1536MiB`, one compiler, and D: build/temp caches did not bound the compiler's live heap/import metadata. Last check after stopping own compiler: C: 3.4 GB free, D: 21 GB. Free sufficient space/commit capacity before heavy gates; the user deferred that repair.

- Use explicit `/bin/sh`, `login:false`; Go binary `/home/bahadir/.local/bin/go` (module toolchain 1.26.9).
- Own Go caches: `/mnt/d/gregale/.codex-cache/deleted-app-inventory/{go-build,go-tmp}`. Use one heavy gate at a time; no concurrent Go compile and full frontend check/build.
- The server package includes hundreds of production/internal/external test files. Consider a focused Go overlay/named-file harness that preserves exact production code and needed fixtures to observe API RED before the full gate. This harness has not been implemented; focused passes would not replace final required gates.
- Windows npm is available through `cmd.exe`. Put task TEMP/TMP on D:, bound Node heap and Vitest workers. Do not clean shared caches/files or restart WSL/other processes without authorization.
- Only own D: cache files received nondestructive page-cache eviction hints; no shared files/caches/processes were changed.
- Private PostgreSQL 16.15 fixture is **stopped**. Extracted runtime: `/home/bahadir/.cache/gregale-k-pg-runtime/root/usr/lib/postgresql/16/bin`; private UTF8 cluster: `/home/bahadir/.cache/gregale-k-pg-runtime/data-utf8`, port 55439, JIT off, max locks 1024. Test DSN: `postgres://bahadir@127.0.0.1:55439/postgres?sslmode=disable`. Own snapshot DB `gregale_k_schema_c010a21` is retained recoverably. No customer database used.

## Release checklist still outstanding

- K: backend API/SDK, post-rebase tests/full gates/final review; frontend inventory/review/reconciliation and invalidations; behavioral RED/GREEN, check/build, failure/context/mobile/history mock walkthroughs.
- L1: sanitized backend declared companion read contract, then bounded frontend journey and verification.
- L2: accurate capability/streaming/client/auth/CLI guidance, then bounded frontend journey and verification.
- M: bounded read-only contract plan; keep unsupported mutations gated; obtain separate staging/native qualification before authorized controls.
- Refresh PR/check/issue state before any future integration. Preserve stack order and only close completed child issues after default-branch integration. Parent #134 remains open until outstanding work and stated qualification gates are resolved.

Do not merge anything as part of this handoff. Mock verification never proves provider, scheduler, network fabric, or staging health.
