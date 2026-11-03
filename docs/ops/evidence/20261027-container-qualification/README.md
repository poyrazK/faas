# Container qualification on main (2026-10-26/28)

Source: main at `aae58d373b` plus the real-image smoke test and a test race fix from this change (run revision `9d04ebc0ef` and follow-ups). Main includes the container gap fixes #5453, #5454, #5455, #5457 and #5516, and the guestdisk fixes #5502 and #5503.

Host: `gregale-internal-test-1`. It has 2 vCPUs, nested KVM and Firecracker 1.7.0, and is shared with other sessions. The run took the shared `/run/lock/faas-builder-acceptance.lock` and was bracketed by leakcheck. The harness is `/mnt/e2e-scratch/cq-20261024/cq2.sh`, copied unmodified apart from a 12-hour lock wait. `cq-gap-launch.sh` and `cq-extra.sh` in this directory started the runs.

**Nested virtualization is not a supported acceptance environment** (`CLAUDE.md`). This is functional evidence. It is not the native acceptance the GA promotion rule requires; the dedicated native host is down because GCP billing is disabled.

## Guest environment (Linux, root, no KVM)

| Check | Result |
|---|---|
| `TestGuestTmpKeepsImageFiles` (metal): image `/tmp` files stay visible under the tmpfs overlay | PASS |
| `guest/init` package tests | PASS, after this change's ordering fix to `TestWorkloadSecretReloadPublishesAndReportsItsOwnIdentity` (ten runs). The fake host recorded a request only after answering it, and the loaded host reordered the requests. |
| `TestFunctionExecutionProxy…`, `TestResumeAcceptSurvivesTransientErrors` | PASS with a short `TMPDIR`. They fail under a long `TMPDIR`, because the Unix socket path exceeds 108 bytes. |
| `make test-container-guest-contract`'s script (2026-10-28, main `c2d9c248c6`), under a temporary delegated cgroup v2 parent with memory and cpu, removed afterwards with no leftover children | PASS: 30 passed, 0 skipped. It covers UID/GID transitions with distinct groups, atomic cgroup launch that fails closed, CPU and memory leaf limits, healthcheck timing and placement, and companion `/tmp` scratch capacity, ENOSPC, isolation and cleanup. This is the first execution of the scratch contract on Linux. |

## Real images (`TestRealImageSmokeMetal`, new)

Each image is pulled from Docker Hub through `mirror.gcr.io` (ADR-20261025114554), pinned by digest, and deployed as a full-rootfs Scale-plan app with open public auth. Each one serves through the gateway, scales to zero on the idle timeout, and serves again after a snapshot restore. **All PASS**; leakcheck is clean.

| Image | Live after | Served after wake | Checks |
|---|---|---|---|
| `nginx:1.27-alpine` | 6m46s | 25 ms | Welcome page |
| `python:3.12-alpine` (probe) | 6m1s | 84 ms | POSIX shared memory (32 MB) and a pty open. `localhost` resolves to 127.0.0.1. The hostname (`10.0.0.2`, set by kernel `ip=` autoconfig) resolves. `RLIMIT_NOFILE` is 65536/1048576. HOME is `/root` (the image has no USER). PATH is the image's. All identical after restore. |
| `eclipse-temurin:21-jdk-alpine` | 14m15s | 34 ms | `InetAddress.getLocalHost()` succeeds |
| `zenika/alpine-chrome:124` | 13m47s | 50 ms | Headless Chromium renders a page (it needs `/dev/shm`), before and after restore |
| `traefik/whoami:v1.10.1` (2026-10-28) | 5m59s | 35 ms | A static Go binary `FROM scratch`, with no shell and no `/etc/passwd`, starts as the image declares it and reports the guest's `10.0.0.2` |
| `prom/prometheus:v2.53.0` (2026-10-28) | 9m25s | 38 ms | `USER nobody`, resolved by name from the image's `/etc/passwd`, starts and serves while writing its TSDB to the image's `/prometheus` |
| `httpd:2.4.62-bookworm` (2026-10-28) | 9m3s | 27 ms | Apache httpd on Debian (glibc) serves "It works!" with the image's default command |
| `node:22-bookworm-slim` (probe, 2026-10-28) | 7m52s | 43 ms | A cmd-only override (`-e <script>`) runs through the image's `docker-entrypoint.sh`, which prepends `node`. Node 22.23.4 matches the image's `NODE_VERSION`. The process loaded glibc 2.36. HOME is `/root`; PATH is the image's. Identical after restore. |

The times to live include image pulls and full-rootfs assembly on the shared nested host. The `whoami` and `prometheus` rows ran at revision `2f143fd581` (main plus those cases) in `/mnt/e2e-scratch/cq-rerun-20261027`, attempt 4. The `httpd` and `node` rows ran at `45d97998c6` (main plus those cases) in `/mnt/e2e-scratch/cq-debnode-20261028`, attempt 1. Leakcheck was clean on both.

Image qualification matrix (`docs/container-qualification.md`) cells these runs touch:
- **Covered here:** static binary in a minimal root (`whoami`), named non-root user (`prometheus`), Alpine/musl and Debian/glibc servers, Node, Python and JVM servers, nginx, and multi-platform index resolution (the official images are indexes; the amd64 manifest is selected).
- **Covered by other metal tests:** multiple TCP and UDP declarations (`TestTCPIngressMetal`, `TestUDPIngressMetal`) and a main application with a companion (`TestDirectOCIMainHealthcheckWithCompanionMetal`).
- **Not covered by a public smoke test:** private-registry pulls, which need credentials.

## Containers lane (`e2e-native` `containers` + image-run phase)

8 of 19 lane tests and 2 of 4 image-run tests passed; leakcheck was clean.
- Passed: `TestDirectOCIFullRootfsMetal`, `TestDirectOCIProcessContractMetal`, `TestDirectOCIPort3000Metal`, `TestUDPIngressMetal`, `TestE2E_Streaming_Metal_TxBytesAccuracy`, `TestBeforeCheckpointMetal`, `TestInteractiveAppTaskMetal`, `TestImageRunsMetal` and `TestImageRunProcessMetal`.
- No failure traces to the container changes. The failures fall into these groups:
  - **Host speed.** Deployments sat in `imaging` or `snapshotting` past the tests' 60–75 s deadlines. Streaming TTFB measured 3.4 s and 1.04 s against a 1 s ceiling. Which tests pass changes from run to run: the other session's attempt 15 on the same host failed `TestDirectOCIFullRootfsMetal` and passed `TestDirectOCIMainHealthcheckMetal`.
  - **Failing the same way in attempt 15, and outside this change** (each one is resolved in the follow-up below):
    - `TestTCPIngressMetal`: readiness probes `/healthz` on port 5432 and is refused. The host now routes readiness to the deployment port (#5416).
    - `TestJobTaskLiveOutputMetal` and `TestInteractiveImageRunMetal`: 503 `traffic_revocation_unavailable`.
    - `TestE2E_Streaming_Metal_PlanMatrix`: 401 on Pro and Scale, which default to bearer public auth (ADR-080). It also hit a Free-plan layer of 509 MB against the 256 MB cap.
    - `TestE2E_Streaming_Metal_QuotaNonCounting`: `X-RateLimit-Remaining` did not drop.

## Finding: a wake after an explicit park can be torn down

`POST /v1/apps/{slug}/park` sets the app `evicted_cold`. In the first smoke attempts, the next request's wake started a snapshot restore. About 3 s later its context was cancelled ("wait for wake network: context canceled", or "read resume ack: use of closed network connection"). The reaper's `parked app reconcile` then destroyed the in-flight wake, and the request got 503 `internal`. This happened for nginx, Python and Java. The lane's park/wake tests pass because their small VMs restore before it happens. Waking after an idle park, which does not set `evicted_cold`, works for all four images.

The cause was that the gateway's deployment-routed wake and the raw TCP/UDP edges admit through schedd's `AdmitInstance` RPC, which never claimed the parked → active lifecycle that `EnsureWake` claims. #5672 makes request admission un-park the app first.

## Follow-up on main (2026-10-27)

| Failure above | Cause | Fix | Nested-host re-run |
|---|---|---|---|
| Wake after an explicit park torn down | Request admission left the app `evicted_cold` | #5672 | `TestBeforeCheckpointMetal` and `TestAfterRestoreMetal` PASS |
| `TestTCPIngressMetal` refused | Source builds default to HTTP `GET /healthz` readiness, which a raw TCP service cannot answer. Only the probes before the bind were refused; later non-HTTP replies went uncounted. | #5672, ADR-20261027090000: a routed port declared as a public TCP listener uses TCP readiness | PASS |
| `traffic_revocation_unavailable` on job logs and attach | Platform responses lacked the traffic-security snapshot through the public hop | #5611 (merged after this run's build) | `TestJobTaskLiveOutputMetal` and `TestInteractiveImageRunMetal` (pipes, tty, disconnect) PASS, 2026-10-28 |
| PlanMatrix 401 on Pro/Scale | Test bug: `require_authn: false` leaves `public_auth_mode=bearer` | #5703 | Hobby, Pro, Scale PASS |
| PlanMatrix Free 509 MB layer | **Product gap**: without ADR-1500's shared runtime (`FAAS_SOURCE_APP_SHARED_RUNTIME`, off everywhere), a Node source app's layer is the whole Railpack image | open; the default is off until ADR-1500's native acceptance | still FAIL |
| QuotaNonCounting drop 0 | Test bug: Hobby's bucket refills 20 tokens/s, so it was full again after every stream | #5703 | PASS |
| `TestAfterRestoreMetal` park 503 | Test bug: a 512 MB capture takes 6.5–7.3 s here, apid waits 5 s and answers a retryable 503 that `api.Client.park` retries. The test did not. | #5753 | PASS |
| `TestAfterRestoreMetal` first wake 503 "wake failed" (one of three runs) | Unknown: the gateway refused it in 17 ms and logged no cause | #5748 now logs the cause of such a wake | did not recur |

Re-runs used revisions `1380fa85c0`, `92961f1c5a` and `92514a2b61` (main plus the open fix) in `/mnt/e2e-scratch/cq-rerun-20261027`. Leakcheck was clean on all three. The #5611 re-run used main `c2d9c248c6` in `/mnt/e2e-scratch/cq-rerun-20261028`, attempt 1, with leakcheck clean.

## Remaining gates on the shared test host (2026-10-28/29, main `bd5c7e719b`)

These runs tried the remaining container gates on `gregale-internal-test-1`. It is still a nested 2-vCPU host, so none of this is native acceptance. Other sessions' Go builds and unit tests do not take the acceptance lock, and they compete for its CPU and scratch disk during a run. The harness checks for a quiet host only before it waits for the lock.

### Containers lane, image runs and lifecycle (`/mnt/e2e-scratch/cq-ga-20261028`)

- **Attempt 1 is invalid.** The run started 2 h after its quiet check, with 14.7 GB of scratch free, below builderd's 20 GiB build-drive floor. Every source build was refused with `insufficient host free space for build drive`. Its image-run phase is valid: `TestImageRunsMetal`, `TestImageRunProcessMetal`, `TestJobTaskLiveOutputMetal` and `TestInteractiveImageRunMetal` all PASS.
- **Attempt 2 is invalid.** CPU pressure was 93% (load 9.4) from other sessions. Builds reached the 15-minute ceiling with no build log, and 19 of 21 tests failed. Two builder VMs it leaked were reaped later.
- **Attempt 3:** 9 of 21 pass, with leakcheck clean.
  - **PASS:** `TestInteractiveAppTaskMetal`, `TestDirectOCIFullRootfsMetal`, `TestDirectOCIMainHealthcheckWithCompanionMetal`, `TestDirectOCIPort3000Metal`, `TestE2E_Streaming_Metal_QuotaNonCounting`, `TestTCPIngressMetal`, `TestUDPIngressMetal` and `TestSourceDeployWakeMetal`.
  - **Host speed:** deployments sat in `imaging` past their 75 s deadlines (`ProcessContract`, `MainHealthcheck`, both §11 fences, `TxBytesAccuracy`, `BeforeCheckpoint`). Streaming TTFB was 2.7 s and 3.0 s against a 1 s ceiling. `TestDeployHealthcheckMetal` missed its 90 s park deadline.
  - **Builder egress:** `TestAfterRestoreMetal`'s builder VM timed out resolving `ghcr.io` through 1.1.1.1 while pulling the platform base image, and no layer downloaded. The log has four such DNS timeouts. The host itself resolved and reached `ghcr.io` in 80 ms, and its NAT and forward rules for `10.100.0.0/16` were in place.
  - **Test bug:** `TestDirectOCIAutoscaleScaleToZeroMetal` went live, then its anonymous request got 401. A Pro app keeps bearer public auth under `require_authn: false`. #5985 fixes it and `TestDeployHealthcheckMetal`'s same setup.

### Companion and fcvm lifecycle (`run-native-metal-smoke.sh`)

The host carries `/etc/faas/builder-acceptance-host` and has no faas services, so the CI runner was used as written, with this host's kernel (6.1.134) and Firecracker (1.7.0).

- **Attempt 1 is invalid.** The runner does not park another session's native ownership journal (`/srv/fc/jail/.native-processes`), so every metal manager refused to start (`existing ownership journal requires native_process_recovery`).
- **Attempt 2** used a copy of the runner that parks the journal the way the cmd/e2e harness does. Result: 1,644 passed, 90 skipped and 28 failed, and the final leakcheck failed.
  - The first failures are `unmount owned image: exit status 32` during teardown. `umountRetryBusy` gives up after 1 s (`pkg/fcvm/umount_retry.go`), while a killed VM's drive stays busy until its process exits.
  - The leaked mounts then fail every later test's own leakcheck, including all five companion tests. The companion gate is therefore not qualified.
  - Eight leaked jail chroots (1.9 GB of tmpfs, 24 bind mounts) were removed by hand afterwards, and leakcheck was then clean.
  - `TestMetalOperatorParkCapture` failed because the run's `TMPDIR` pointed outside `/tmp`. That is a harness difference, not a product failure.

### Findings

- **Build failure blamed on the user.** builderd classified the builder's DNS failure fetching the platform's own base image as `failure_class: user_error`. A platform network failure should not be reported to the customer as their build error.
- **Slow teardown leaks mounts.** A 1 s unmount budget leaks a VM's bind mounts and jail whenever process exit is slow. A loaded node can hit this too.
- **Harness gap.** The `cq-ga-20261028` copy of `cq2.sh` now re-checks scratch and root free space after taking the lock (`DISK_LOW_AFTER_LOCK`). Nothing can stop other sessions' unlocked builds from starving a run mid-way.
