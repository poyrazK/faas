# Handoff: make disposable Runs (`gregale run`) operable

Branch: `feat/runs-gaps-rebased`, rebased on `origin/main` @ `a1c5b23f4b` (2026-10-13).
It supersedes `feat/runs-gaps`, the same work on an older base; that branch was not force-pushed.
Status: code complete for gaps 2–6 and unit-tested. The real-VM metal test (gap 1) still fails at the
Firecracker config step; see **Next steps**. No PR has been opened: the owner opens PRs only on
explicit request.

## Background

Before this work, a review of `gregale run` (ADR-171) found:

1. No metal or e2e test exercised a real restore→execute→destroy.
2. Nothing published runtime snapshots, so every Run would cold-boot.
3. Runs were never billed.
4. Enablement needed about 40 hand-set `FAAS_EXECUTION_<RUNTIME>_*` values, and nothing produced the execution layer.
5. Receipts were never deleted.
6. The 30 s deadline was too narrow for general compute.

In prod Runs are disabled: the apid gate is `0`, there are no `FAAS_EXECUTION_*` values on compute, and `executions` has 0 rows.

## What the branch does

| Gap | Change | Key files |
|---|---|---|
| 5 Retention | Receipts expire after 7 d (`api.ExecutionReceiptRetention`). schedd purges them in batches every 10 min, also when dispatch is off. The usage ledger no longer cascades from `executions`. Receipts without a usage row, or belonging to an active managed workflow, are kept. | `migrations/20261013090000001_*`, `pkg/state/{queries.sql,pgstore_executions.go,memstore_executions.go}`, `pkg/sched/execution_coordinator.go` (`PurgeExpiredOnce`, `RunRetention`), `cmd/schedd/main.go` |
| 3 Billing | A Run is billed as (memory + 8 MB) × started seconds from `running` to terminal, using host timestamps. It is written as one `usage_minutes` row with `meter_kind='run'` and `execution_id`, in the terminalization tx (`ExecutionComputeUsageRecord`). `usage_daily` gets one Runs row per account and day, keyed by the account ID. Financial evidence names the workload `runs`. The API (`/v1/usage`, `/v1/usage/daily`) gains `kind`, and the CLI labels the row `runs`. SDKs are regenerated. | `migrations/20261013090000002_*`, `pkg/state/pgstore.go` (coalesce adds `account_id`), `pkg/meter/rollup.go`, `pkg/apid/apidserver/handlers_ext.go`, `pkg/api/dto.go`, `api/openapi.yaml` + embed, `sdk/*`, `cmd/gregale/usage_app_labels.go` |
| 4 Enablement | schedd derives artifacts from storage (`cmd/schedd/execution_artifacts.go`): kernel `kernel/<fc>` (hashed once), base + `.digest` sidecar (config digest + guest-init digest, which is the executor identity), and execution layer `execution/layer-v1-<profile>-<arch>.ext4`. imaged stages that layer at startup (`pkg/imaged/execution_layers.go`). Env vars remain an explicit per-runtime override. A single Ansible switch, `faas_runs_enabled`, sets the apid and schedd gates (drop-ins `zz-faas-runs.conf`). | `cmd/imaged/main.go`, `pkg/sched/paths.go`, `deploy/ansible/roles/{control_plane_service,compute_only_service}` |
| 2 Snapshots | A catalog miss queues a background payload-free capture (`pkg/sched/execution_snapshot_capturer.go`): boot a dedicated VM, then `PauseAndSnapshot`, then `RegisterStoredRuntimeSnapshot`. `Manager.Park` waits for the resume-hook ack before capturing an execution VM. Execution guests now bind the resume hook (after the execution listener and relay), so restores re-seed entropy and step the clock. Verification results are cached. vCPU count is fixed at `api.ExecutionVCPUCount = 2`. `SnapshotRef.MemBytes` is forwarded. | `guest/init/execution_linux.go`, `pkg/fcvm/manager.go`, `pkg/sched/{runtime_snapshot_publisher.go,runtime_snapshot_storage_verifier.go,execution_claim_resolver.go}` |
| 6 Deadlines | Hobby 1 min / Pro 5 min / Scale 15 min; hard max 900 s. Includes a DB CHECK migration, and `gregale run --wait` now defaults to 20 min. | `pkg/api/limits.go`, `migrations/20261013090000003_*`, `api/openapi.yaml`, `cmd/gregale/commands_runs.go` |
| 1 Metal | New test `TestMetalDisposableRunsColdRestoreAndIsolation`. It builds a python base from host python3 plus guest-init, and covers a cold Run, a deadline, a capture, and two restores, checking distinct RNG, stepped clock, loopback-only networking, non-root, and no leaks. Found and fixed one real bug: networkless jails raced the cgroup fence and had no usable `/dev/kvm` (nodev). There is now a `--setup-jail-kvm` helper mode and `prepareNetworklessJail`. | `pkg/fcvm/execution_runs_metal_test.go`, `pkg/fcvm/vmm.go`, `pkg/jailsetup/run_linux.go` |

Decisions are recorded in **ADR-986** (billing, retention, snapshots, artifacts) and **ADR-996** (deadlines).
ADR-171 is marked accepted. Docs are updated in `docs/executions.md`.

## Verified (on `gregale-internal-test-1`, n2 nested KVM)

- Build of `./pkg/... ./cmd/{schedd,imaged,vmmd,meterd,gregale} ./guest/...`: OK.
- Tests pass for `pkg/api`, `pkg/sched`, `pkg/imaged`, `cmd/schedd`, `pkg/meter`, `guest/executor`, `pkg/jailsetup`, and full `pkg/fcvm` (unit).
- `pkg/state` subset passes against real Postgres 16: Execution, Purge, Usage*, Financial, RuntimeSnapshot, Migration, Clone, Checkpoint.
- A `schema.sql` regenerated with `cmd/schema-dump` shows no drift from this branch. The only diff is main's own environment-workload functions.
- `sqlc generate` produces no diff. The Node and Python SDKs are regenerated (only the affected models).

## Next steps (in order)

1. **Firecracker rejects the networkless config.** The last metal run boots the jail fine (`wake ok`, cold boot 3.8 s), then FC exits with: `Invalid JSON: invalid type: null, expected a sequence at line 1 column 833`.
   - Cause: `pkg/fcvm/config.go:21` `NetworkInterfaces []NetIface \`json:"network-interfaces"\`` serializes a nil slice as `null` when `Networkless`.
   - Fix: emit `[]` (set an empty slice in the networkless branch near `config.go:435`, or add `omitempty`; check FC 1.7 accepts an omitted key). Add a unit test that marshals a networkless config.
   - The cold-boot path reports success even though FC died, because `SkipReady` is set for execution VMs. Consider failing the wake when the FC process exits before the execution dial.
2. Re-run the metal test until green. Fix whatever surfaces next in this order: python base / executor, timeout, capture (`Park` → `awaitExecutionCaptureReady`), restore (`Snapshot.Networkless`, `MemBytes`), distinct RNG across restores.
3. After the run, check for the leftover `/firecracker (deleted)` process that leakcheck reported. It may be ours from the failed boot; clean it up and re-verify there are no new leaks.
4. When metal is green:
   - Point `pkg/productcap/catalog.json` → `disposable-runs.acceptance_test` at `pkg/fcvm/execution_runs_metal_test.go::TestMetalDisposableRunsColdRestoreAndIsolation`.
   - Regenerate `docs/capabilities.md` (`cmd/capabilities-md`).
   - Add a `docs/STATUS.md` entry with the n2 evidence. n2 results count as evidence; do not add a "native run pending" caveat.
5. Run the CI gates locally or in CI: `make sqlc-check`, `make spec-check`, `make sdk-gen-node-check`, the Python SDK gen check, `make lint`.
6. Squash and push. **Do not open a PR unless the owner asks** (and "release" means "open a PR").

## Not done (left out of scope)

- General guest egress, env vars/secrets for Runs, and `gregale run --image` (container images). Each needs its own ADR. ADR-171 requires an allowlist ADR for any egress.
- `ephemeral_disk_mb` is not enforced. Scratch is the guest `/tmp` tmpfs, which is bounded by memory; the field only selects the snapshot shape.
- Native process recovery (`native_process_recovery`, experimental, off by default) does not support networkless VMs. They now fail closed with an explicit error.
- Enabling Runs in prod (`faas_runs_enabled: true`) has not been done; it is an operator decision.

## Known environment noise (not from this branch)

- `guest/init` `TestResumeAcceptSurvivesTransientErrors` fails on the n2 node with `bind: invalid argument`. It is the test's own Unix-socket bind, which this branch does not touch.
- leakcheck on the node is dirty before every run because of other sessions' jails and netns. Compare before and after.
- `pkg/sched` and other big compiles get OOM-killed on the shared node unless you use `GOMAXPROCS=1 GOGC=20 GOMEMLIMIT=2500MiB` and `-gcflags='<pkg>=-c=1 -dwarf=false' -ldflags=-w`, one package at a time.

## How to run things on the n2 test node

- Host: `gregale-internal-test-1` (`us-east1-b`, project `gregale-prod`, `--tunnel-through-iap`, account `kucukarslanhuseyinpoyraz@gmail.com`, user `kucukarslanhuseyinpoyraz_gmail_c`).
- Work dir: `/mnt/e2e-scratch/runs-gaps-20261011/` (`src/` holds the tree, `.base` records the commit).
- Private Postgres 16: `127.0.0.1:15437`, superuser `postgres`, DB `faas_test` owned by role `faas`. Data is in `pg/`; start it with `sudo -u postgres /usr/lib/postgresql/16/bin/pg_ctl -D <dir>/pg/data -o "-p 15437 -k <dir>/pg ..." start`.
- Go: `GOTOOLCHAIN=go1.26.9 GOPROXY=off GOFLAGS=-mod=mod`. The node has no module proxy, so if main adds modules, `go mod download` them locally and untar `~/go/pkg/mod/cache/download/<module>/@v` into the node's `$(go env GOMODCACHE)/cache/download`.
- If `/usr/bin/ssh` fails inside a long job (`No user exists for uid 501`), use a pure-Go SSH client over `gcloud compute start-iap-tunnel <vm> 22 --listen-on-stdin` (`golang.org/x/crypto/ssh` with known_hosts alias `compute.<instance-id>`, host key algorithm `ssh-ed25519`).
- Push a tree: `git archive HEAD | gzip | <ssh> 'cd $D && rm -rf src && mkdir src && tar -xzf - -C src'`.
- Metal run (as used; run detached with `nohup setsid`):

```bash
cd $D/src; M=$D/metal; mkdir -p $M/tmp
export GOTOOLCHAIN=go1.26.9 GOCACHE=$D/gocache GOTMPDIR=$D/gotmp GOPROXY=off GOFLAGS=-mod=mod GOMAXPROCS=1 GOGC=20 GOMEMLIMIT=2500MiB
CGO_ENABLED=0 go build -trimpath -o $M/vmmd ./cmd/vmmd
CGO_ENABLED=0 go build -trimpath -o $M/vmmd-jail-helper ./cmd/vmmd-jail-helper
CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -o $M/guest-init ./guest/init
go test -c -tags metal -o $M/fcvm.test ./pkg/fcvm
sudo bash deploy/scripts/leakcheck.sh > $M/leak.before 2>&1
sudo flock -w 7200 /var/lock/faas-builder-acceptance.lock env \
  TMPDIR=$M/tmp FAAS_TEST_VMMD_BINARY=$M/vmmd FAAS_TEST_GUEST_INIT_BIN=$M/guest-init \
  FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-6.1.134 FAAS_TEST_FC_VERSION=1.7.0 PATH=$M:$PATH \
  $M/fcvm.test -test.run '^TestMetalDisposableRunsColdRestoreAndIsolation$' -test.v -test.count=1 -test.timeout 25m
sudo bash deploy/scripts/leakcheck.sh > $M/leak.after 2>&1; diff $M/leak.before $M/leak.after
```

- The jailer's stderr for a failed boot is in `/var/log/faas/vm-<instance>.console` on the node. The guest console tail is printed by the metal helper on failure.
- Respect `/var/lock/faas-builder-acceptance.lock`; other sessions hold it for long runs. Keep binaries out of `/tmp`, because the root disk is nearly full.
