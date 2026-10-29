# Logs live and archive metal evidence

The `logs` capability is `ga`. Until this run its only acceptance evidence was
a CLI client test against an `httptest` fake. `TestLogsLiveAndArchivedMetal`
(`cmd/e2e/logs_archive_metal_test.go`) checks the customer path on a native
KVM host:

1. A Node app is built from source in a builder VM.
2. A request writes a unique marker to the app's stdout.
3. The marker is read back from the running instance through
   `GET /v1/apps/{slug}/logs` (gatewayd-internal → schedd → vmmd ring).
4. The app is parked. vmmd retires the ring into its spool, and the shipper
   uploads the day to an in-process S3-compatible bucket.
5. The same line is read back through
   `GET /v1/apps/{slug}/logs?archive=1&instance=…&date=…`, which ends in
   `archive_complete`.

## Run

| | |
|---|---|
| Host | `gregale-internal-test-1` (nested KVM), under the shared `/var/lock/faas-builder-acceptance.lock` |
| Source | `7f25dce103` (#5515 on main `beff15ec32`, which includes #5503) |
| Scripts | `logs-e2e-run.sh` builds and runs; `logs-e2e-rerun.sh` rebuilds only `e2e.test` and calls `logs-e2e-test.sh`; `logs-e2e-inner.sh` runs under the lock |
| Lock acquired | 2026-10-26 19:09 UTC |

| Check | Result |
|---|---|
| `TestLogsLiveAndArchivedMetal` | PASS (625 s) |
| leakcheck before and after | one netns, `fc-cbf681c6-…`, present **before** the run and unchanged after it (left by an earlier session); nothing new |

Native CI could not run this: billing is disabled on the CI project.

## What the earlier runs found

1. **Build export failed on every production-shaped node.** #5408 refuses to
   edit root-owned drives. A builder's scratch drive, bind-mounted when the jail
   is tmpfs and the drive is on disk, stayed root-owned (`layer.ext4 is owned by
   root; refusing to edit it as root`). Fixed by #5503 (the same change as the
   superseded #5490).
2. **Not enough free scratch space for a build drive.** builderd needs 20 GiB
   free; the host's scratch disk was 17 MB short. About 91 GB of stale caches
   and run output was cleaned on 2026-10-26.
3. **The test expected the wrong park status.** apid parks synchronously and
   answers 204; the test only accepted 202. The live-logs half had already
   passed in that run.
