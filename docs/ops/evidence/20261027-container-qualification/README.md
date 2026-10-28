# Container qualification on main (2026-10-26/27)

Source: main at `aae58d373b` plus the real-image smoke test and a test race fix from this change (run revision `9d04ebc0ef` and follow-ups). Main includes the container gap fixes #5453, #5454, #5455, #5457 and #5516, and the guestdisk fixes #5502 and #5503.

Host: `gregale-internal-test-1`. It has 2 vCPUs, nested KVM and Firecracker 1.7.0, and is shared with other sessions. The run took the shared `/run/lock/faas-builder-acceptance.lock` and was bracketed by leakcheck. The harness is `/mnt/e2e-scratch/cq-20261024/cq2.sh`, copied unmodified apart from a 12-hour lock wait. `cq-gap-launch.sh` and `cq-extra.sh` in this directory started the runs.

**Nested virtualization is not a supported acceptance environment** (`CLAUDE.md`). This is functional evidence. It is not the native acceptance the GA promotion rule requires; the dedicated native host is down because GCP billing is disabled.

## Guest environment (Linux, root, no KVM)

| Check | Result |
|---|---|
| `TestGuestTmpKeepsImageFiles` (metal): image `/tmp` files stay visible under the tmpfs overlay | PASS |
| `guest/init` package tests | PASS, after this change's ordering fix to `TestWorkloadSecretReloadPublishesAndReportsItsOwnIdentity` (ten runs). The fake host recorded a request only after answering it, and the loaded host reordered the requests. |
| `TestFunctionExecutionProxy…`, `TestResumeAcceptSurvivesTransientErrors` | PASS with a short `TMPDIR`. They fail under a long `TMPDIR`, because the Unix socket path exceeds 108 bytes. |

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

The times to live include image pulls and full-rootfs assembly on the shared nested host. The two 2026-10-28 rows ran at revision `2f143fd581` (main plus those cases) in `/mnt/e2e-scratch/cq-rerun-20261027`, attempt 4, with leakcheck clean.

Image qualification matrix (`docs/container-qualification.md`) cells these runs touch:
- **Covered here:** static binary in a minimal root (`whoami`), named non-root user (`prometheus`), Alpine/musl servers, Python and JVM servers, nginx, and multi-platform index resolution (the official images are indexes; the amd64 manifest is selected).
- **Not yet covered by a real image:** Debian/glibc servers and a Node server.
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
| `traffic_revocation_unavailable` on job logs and attach | Platform responses lacked the traffic-security snapshot through the public hop | #5611 (merged after this run's build) | not re-run |
| PlanMatrix 401 on Pro/Scale | Test bug: `require_authn: false` leaves `public_auth_mode=bearer` | #5703 | Hobby, Pro, Scale PASS |
| PlanMatrix Free 509 MB layer | **Product gap**: without ADR-1500's shared runtime (`FAAS_SOURCE_APP_SHARED_RUNTIME`, off everywhere), a Node source app's layer is the whole Railpack image | open; the default is off until ADR-1500's native acceptance | still FAIL |
| QuotaNonCounting drop 0 | Test bug: Hobby's bucket refills 20 tokens/s, so it was full again after every stream | #5703 | PASS |
| `TestAfterRestoreMetal` park 503 | Test bug: a 512 MB capture takes 6.5–7.3 s here, apid waits 5 s and answers a retryable 503 that `api.Client.park` retries. The test did not. | #5753 | PASS |
| `TestAfterRestoreMetal` first wake 503 "wake failed" (one of three runs) | Unknown: the gateway refused it in 17 ms and logged no cause | #5748 now logs the cause of such a wake | did not recur |

Re-runs used revisions `1380fa85c0`, `92961f1c5a` and `92514a2b61` (main plus the open fix) in `/mnt/e2e-scratch/cq-rerun-20261027`. Leakcheck was clean on all three.
