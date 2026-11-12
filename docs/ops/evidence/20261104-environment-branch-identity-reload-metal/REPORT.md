# Environment branch identity-reload restore on metal, 2026-11-04

ADR-20261031193635 slice 5 needs `test-metal` for its VM paths. Both ran on
`gregale-internal-test-1` (GCP n2, 2 vCPU, KVM, cgroups v2) with the native
harness (`/mnt/e2e-scratch/e2e-run.sh`, shared acceptance lock, leakcheck
before and after).

## New acceptance: `TestEnvironmentBranchIdentityReloadMetal` — PASS

Branch `feat/environment-branches` at `e286a5a016`, 598 s, leakcheck clean
before and after.

1. A source-deployed Node app opts in with
   `after_restore: { path: /internal/after-restore, identity: reload }`. Its
   production env sets `APP_COLOR=production`.
2. Branch `pr-1` (environment branches released through runtime config):
   the first request cold-boots under the branch's own identity, serving
   `color=production reloads=0` (the branch copied production's values), and
   queues the parent deployment's credential-free capture.
3. schedd's capture worker boots the parent on a quarantined fork-mode
   instance with placeholder managed secrets, takes the capture and retires
   the VM: `environment branch capture: ready ... attempts=1`. No `snapshots`
   row holds it.
4. Branch `pr-2` gets `APP_COLOR=branch-two` and wakes for the first time.
   schedd logs `wake: restoring the lineage parent's credential-free
   capture`; the wake method is `restore`. guest-init fetches the branch's
   identity document from vmmd, writes it to
   `/tmp/gregale-identity/identity.json`, and calls the hook with the file
   path and digest. The hook reloads, answers with the digest, and the
   restored process serves `color=branch-two reloads=1`. vmmd never logged
   `restore identity not acknowledged`; the capture stays `ready`.

## Regression set at `6a7cb7445b`

| Test | Result |
|---|---|
| `TestAfterRestoreMetal` (with reject and timeout) | PASS |
| `TestFrozenTierMetal` | PASS |
| `TestBeforeCheckpointMetal` | PASS on re-run (first run hit its 15 min build ceiling) |
| `TestForkMetal` | PASS on re-run (first run hit its 90 s deploy-live deadline) |
| `TestSourceDeployWakeMetal` | restore, re-park, stale-snapshot cold boot and corrupted-vmstate fallback all passed; the test then stopped on the node's missing `FAAS_COMMIT_TLS_PG_BIN_DIR` |
| `TestCrashSnapshotMetal` | FAIL three times on its 90 s deploy-live deadline, before any capture: imaging alone took over 90 s on this host (app layer ~21 s, secret scan ~20 s, grype > 58 s) |

## Harness notes

- A branch's environment host is built with `hostidentity.DeploySuffix()`, not
  the harness's apps domain, and serves only once the branch deployment is
  live; branch workloads require authentication, so the test sends the
  account's API key.
- builderd needs 20 GiB free for a build drive. The scratch disk dropped to
  19.9 GB during these runs, and the build then stalled with no output;
  removing earlier run directories fixed it.
