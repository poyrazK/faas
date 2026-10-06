# Managed PostgreSQL main integration verification — 2026-10-03

This capture covers the integrated managed PostgreSQL reliability branch. The internal node uses GCP nested virtualization; these results are diagnostics. Customer cutover remains disabled. Native x86_64 KVM lifecycle/reboot acceptance and filesystem power-loss qualification remain outstanding.

## Source and host

- Initial integration: main `fae3c926ce13e017035de70907548efa436300c9`, merge `9bc1e674ff8ecb4030a53208af60a9e3abfe0239`.
- Initial reconciled schema snapshot: `9917c420f2ff484ebf87a6379e791a3c852fe911`.
- PostgreSQL race and lost-ledger replay snapshot: `a1d5254cadc4998f4ffac970b625329e30638fe9`.
- Full macOS fcvm/vmmd race snapshot: `8e357870ef88a50883cc5888eccf378eb547bec1`.
- Final internal VM, fresh schema and SQL gate snapshot: `d4155bdc5a1c8f5f3e5c2e68e2447ce4a1c34e38`.
- Main subsequently advanced by one hardware-coverage script PR to `d5a99457f1a4643af0449ea3995b590b9794604d`; merge `067926a17` incorporates it. Its wrapper contract tests passed. The final implementation adds a CI timeout adjustment and evidence, with no change to the tested Go, migrations, protobuf, SQL, SDK or VM runtime inputs.
- Host: `gregale-prod / us-east1-b / gregale-internal-test-1`; Ubuntu 24.04, nested `n2-highmem-2`, Go 1.25.13, Firecracker 1.7.0, PostgreSQL 16.

`validation.json` pins source archives, patches, runner hashes and final commit-derived SHA-256 manifest. The final manifest covers 14,525 regular tracked files, excluding symlinks. It is generated from `git ls-tree` and `git cat-file` of the committed tree, rather than a concurrently regenerating working tree.

## Completed validation

The bounded metal selection passed **99 top-level tests (416 including subtests)**, with zero failures or skips. All three lock-held leak checks passed. All 14,525 committed regular files matched before and after the run, and at dispatch. The explicit metal and full Linux vmmd race exit files are zero; the unit journal confirms completion.

Full Linux vmmd and full macOS fcvm/vmmd race suites passed. Repeated snapshot recovery/destruction cancellation tests passed. Linux-target lint passed with zero issues. `make pre-pr` passed all generation and contract gates; its generated contract inputs remain unchanged by the later journal regression and CI fixture corrections. Node SDK tests (80 checks) and Python SDK tests (124 passed, 1 skipped, 1 heavy regeneration test deselected) passed; pre-pr separately covers generation.

PostgreSQL 16 fresh installation, upgrade from initial integrated main and migration replay produced identical schemas. The final guarded fresh install matches canonical `schema.sql`. The entire managed PostgreSQL race suite passed with SCRAM role logins. Lost-ledger replay passed for all eight new migration versions with their DDL retained. The corrected SQL gate validated 2,583 platform, 13 customer and five qualification statements.

Implementation CI completed 26 successful checks plus one skipped image matrix; every unit/migration/E2E shard, both SDKs, CodeQL and contract gates passed. The combined lint/build job was interrupted by its total time budget after lint, vet and build passed. Its step results are captured in `ci-d415-lint-job.json`; the 30-minute budget adjustment must be validated by final PR CI.

The PostgreSQL suite uses a private local cluster and an unmigrated base database for per-test isolation. Administrative loopback connections use trust; generated credential-role connections require SCRAM. The Neon credential fixture installs a known test password before verifying its returned role, so permission checks do not reuse the administrator's credential.

The SQL gate prepares qualification statements against a separate disposable database and retains exact public qualification. It asserts that platform migrations never install customer Commit or qualification tables. All four E2E CI shards passed on the same implementation commit, including the managed PostgreSQL manifest fixtures. Those fixtures seed ready catalog resources and credentials, run real APID, and make no vendor API calls. They check scoped binding reuse, repeated apply, account isolation and rejection before project mutation.

## Integration fixes and preserved failures

- Schema regeneration reconciled SQLC with the actual PostgreSQL 16 migration replay.
- The starter's release path now uses the migration role; the runtime role receives DML privileges and cannot own tables, create tables or disable RLS.
- New unmerged migrations tolerate missing ledger entries with their DDL still present.
- Startup inventory cancellation stops gracefully only when the parent daemon context is cancelled; independent recovery errors still fail startup. Snapshot failure recovery keeps the registered context that outlives its RPC while Destroy cancels and joins it.
- Main's private process-generation counter is absent from serialized leases. The first integrated metal run found that full struct comparison prevented confirmed cleanup from retiring a reopened journal record. Durable matching now ignores that private counter while fencing PID, start ticks, boot ID and every persisted identity. The regression rejects 14 forged identity variants.
- The initial whole-repository Linux race run and the later full-fcvm retry were OOM-killed in the constrained unit. The latter reached the existing 3 GiB manifest-name fixture. The first run also used a migrated public schema as its shared pgtest base and retains fixture failures; the retry uses an unmigrated base. Their logs and systemd status remain in the raw archive; neither is a passing gate. Bounded metal and full CI shards cover separate scopes.
- The first refreshed run stopped before tests because two SDK checksum entries were collected during regeneration. The committed source bytes were correct. The replacement manifest reads immutable Git blobs; the failed manifest/checks are preserved.
- The first macOS full race and a later pre-pr attempt ran out of disk. Successful retries are captured separately. Initial Python generation required creating the ignored development virtual environment.
- Full Darwin lint reports three unused fields/helpers referenced by Linux-only guest sources unchanged from main. Linux-target lint passes. An initial attempt to cross-compile the linter itself failed with an executable-format error; the final check runs a native linter against the Linux target.
- Latest implementation CI passed lint, vet and build, but its combined job exhausted the 15-minute total budget during nftables tests. The PR raises that bounded job budget to 30 minutes and preserves every step. Final PR CI must finish independently; an interrupted job is not reported as green.
- After the internal fresh-schema and SQL preparation phases passed, the duplicate internal E2E compilation was intentionally stopped because all four E2E CI shards had already passed on the same implementation source. Its interrupted unit and empty/incomplete output are retained and are not counted as an internal E2E pass.

## Artifact integrity and cleanup

The raw `captured-results.tar.gz` preserves all selected server captures and failed attempts. `RAW_SHA256SUMS` records original selected file digests before trailing-whitespace normalization; `SHA256SUMS` covers committed artifact bytes. `download-verification.log` verifies the archive digest and safe member paths before extraction. `cleanup.log` records stopped owned units, the private PostgreSQL shutdown and removal of the owned registered stage. Shared caches and other tasks are outside this cleanup. Inactive/unloaded unit defaults alone do not establish a test pass; explicit exit files, completion logs and the unit journal provide the result.

Prepared-boot crash fixtures inject journal boot provenance and kill fixture processes; they do not reboot the host. Restart survivors remain quarantined. Serving recovery, safe same-boot physical reclamation, complete resource incarnations and durable fleet-wide drain proof remain follow-up work. Preparation/verification does not publish a customer cutover or establish a drain receipt.
