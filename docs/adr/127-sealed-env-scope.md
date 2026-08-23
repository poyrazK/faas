# ADR-127 — Tighten `sealed.env` scope (issue #585)

- **Status:** proposed
- **Date:** 2026-08-23
- **Decision:** `/etc/faas/sealed.env` is loaded only by the `apid`
  systemd unit. Every other control-plane daemon (schedd, meterd,
  githubd, gatewayd-internal, vmmd, imaged, builderd, pg-basebackup-push)
  loads its secrets via one of two mechanisms, **chosen by the
  SHAPE of the env var** (content vs path):

  1. **PATH-shaped env vars** (env var IS the path; daemon does
     `os.ReadFile(env)`) — `systemd LoadCredential=<id>:<path>` plus
     `Environment=KEY=%d/<id>`. systemd copies the file to tmpfs
     `$CREDENTIALS_DIRECTORY/<id>` at activation; `%d/<id>` expands to
     that tmpfs path. Used for: `FAAS_HOST_AGE_IDENTITY_PATH`,
     `FAAS_HOST_AGE_RECIPIENT_PATH` (literal path; vmmd only).

  2. **CONTENT-shaped env vars** (env var IS the content; daemon reads
     content via `os.Getenv` then parses) — per-daemon
     `EnvironmentFile=-/etc/faas/secrets/<daemon>/<daemon>.env` at 0400
     root:root. systemd parses the file at activation, sets each
     `KEY=value` in the process env. Used for: `FAAS_SESSION_KEY`
     (hex), `FAAS_INTERNAL_SVC_KEY_SEALED_BLOB` (base64),
     `FAAS_GITHUB_APP_ID` + `FAAS_GITHUB_APP_CLIENT_ID` +
     `FAAS_GITHUB_APP_CLIENT_SECRET` (strings),
     `FAAS_TLS_DNS_TOKEN` (token), `FAAS_BILLING_*` + `FAAS_PADDLE_*`
     + `FAAS_MAIL_*` (billing-provider strings).

  The bash-only `pg-basebackup-push` one-shot uses
  `LoadCredential=faas_off_host_backup_rclone:...` so rclone can read
  `$CREDENTIALS_DIRECTORY/faas_off_host_backup_rclone` directly (no env
  var involved — rclone takes the config path on the command line).

  The shared cross-daemon `DATABASE_URL` is unchanged — it lives in
  `/etc/faas/compute-db.env` (0440 root:faas) already loaded by
  imaged/builderd/gatewayd-internal/vmmd.

  **The path-vs-content distinction is load-bearing.** Using the
  `LoadCredential+%d/<id>` mechanism for a content-shaped env var
  silently breaks the daemon: `os.Getenv(FAAS_SESSION_KEY)` returns
  a path string, the loader's `hex.DecodeString(raw)` fails, and the
  daemon either falls back to ephemeral mode (silent security
  regression — apid pre-fix) or refuses to start (loud, e.g. schedd
  sealed-blob unseal). This was caught by the PR #1075 code-review
  cycle; the fix is to use `EnvironmentFile=` for content-shaped
  vars and reserve `LoadCredential+%d/<id>` strictly for path-shaped
  ones.

  A new CI gate (`scripts/ci/check_sealed_env_scope.sh`, wired into
  `make lint` and the `lint + build` GitHub Actions job) refuses any
  `EnvironmentFile={-,}/etc/faas/sealed.env` line in a non-apid unit,
  scanning `deploy/systemd/`, `deploy/controlplane/systemd/` (the v1
  cp-cp tombstone that `make generate` still maintains — see
  `pkg/daemonunitspec/registry.go:142`), and every ansible role's
  `files/*.service` mirror.
- **Why:** Spec §11 line 1245 mandates *"secrets in
  `/etc/faas/secrets/` root:root 0400, never in env of tenant-reachable
  processes"*. Today that invariant is **silently violated**: 9
  systemd units load `EnvironmentFile=[/-]/etc/faas/sealed.env`, and
  every daemon on the box inherits **the entire sealed.env** in
  `os.Getenv`-readable form. A `/proc/<pid>/environ` read on schedd,
  meterd, imaged, githubd, builderd, gatewayd-internal, vmmd, or
  pg-basebackup-push yields apid-only secrets — `STRIPE_WEBHOOK_SECRET`,
  `GOOGLE_CLIENT_SECRET`, `GITHUB_CLIENT_SECRET`, `FAAS_PADDLE_API_KEY`,
  `FAAS_PADDLE_WEBHOOK_SECRET`, `FAAS_BILLING_PORTAL_URL`,
  `HETZNER_STORAGE_BOX_*`, `FAAS_AUDIT_HMAC_KEY`,
  `FAAS_MFA_RECOVERY_HMAC_KEY`, `FAAS_HOST_HMAC_KEY`. The threat model
  treats the control-plane daemons as mutually-distrusting (CLAUDE.md
  component ownership: each daemon is a separate uid with its own
  capabilities and its own `/run/faas` listener; nothing on the box
  is supposed to read another daemon's secrets). The current
  `sealed.env` blanket breaks that boundary for free. The issue body
  lists 4 daemons (apid/schedd/imaged/meterd); the actual inventory
  is **9** systemd units — discovery surface from
  `grep -rln 'EnvironmentFile=.*sealed.env' deploy/`.
- **Consequences:**
  - **Daemon binary code does NOT change.** `cmd/<daemon>/main.go`
    reads every env var via `os.Getenv` (or the typed `envOr` /
    `getenv` helpers at `cmd/apid/main.go:96-101`,
    `cmd/schedd/main.go:239-244`, `cmd/imaged/main.go:600-605`).
    systemd populates the daemon's process env at activation time
    regardless of whether the var came from `EnvironmentFile=`,
    `LoadCredential=` resolved via `%d/<name>`, or `Environment=`.
    No Go code changes; only the 8 non-apid unit bodies change.
    **Caveat:** the env-var shape (content vs path) MUST match the
    loader's expectation. See top-of-ADR decision block; using
    `LoadCredential+%d/<id>` for content-shaped env vars silently
    returns a path string and breaks the parser.
  - **New per-daemon secret files** at
    `/etc/faas/secrets/<daemon>/<file>` (mode 0400 root:root) and
    `/etc/faas/secrets/secrets.env` (mode 0440 root:faas, for the
    shared `DATABASE_URL` already covered by the existing
    `compute-db.env` symlink). Provisioned by the existing ansible
    roles (`control_plane_service`, `builderd_service`,
    `compute_only_service`, `githubd_service`,
    `gatewayd_internal_service`, `vmmd_service`, `postgres_backup`)
    mirroring the `host.age.previous` perm-assert pattern at
    `deploy/ansible/roles/control_plane_service/tasks/main.yml:323-334`.
  - **New CI gate** `scripts/ci/check_sealed_env_scope.sh` mirrors
    `scripts/ci/check_systemd_hardening.sh` shape (88 LOC template):
    walks all 4 production trees (`deploy/systemd/` + 3 mirror
    trees including the v1 cp-cp tombstone), uses an `errs` counter
    with stderr-only diagnostics, exit 1 on any non-apid hit. Wired
    into `Makefile:lint` and the GitHub Actions `lint + build` job
    next to the `systemd hardening gate` step. Future PRs that
    re-introduce `EnvironmentFile=/etc/faas/sealed.env` in a non-apid
    unit fail CI loud.
  - **Spec §11 line 1245 amended** to mention the
    `LoadCredential=` / `secrets.env` split + the apid-only invariant.
  - **Migration backout is byte-identical.** Reverting a single unit
    file re-enables the old behaviour; no rollback playbook needed.
    Operator muscle memory on `/etc/faas/sealed.env` is preserved
    for apid (the only legitimate consumer).
  - **Runtime perm check unchanged.** `allowedSecretPerm` at
    `cmd/gatewayd-internal/secrets.go:44-50` continues to enforce
    0o400/0o440/0o600/0o640 for any secret file the daemon reads
    via `os.ReadFile`. The CI gate is a *static* superset; the
    runtime check still fires on per-daemon secret files.
  - **Zero migration footprint** — no new tables, no schema delta.
    Skip `migrations/` and the slot-fence dance entirely.
  - **Zero new metric names.** Existing Prometheus series untouched.
  - **No quota / limit change.** `pkg/api/limits.go` unchanged.
- **Rejected alternatives:**
  - **Move all apid-only sealed.env material into a
    `LoadCredential=` cluster on the apid unit.** Operator muscle
    memory (30+ apid env vars) breaks; the
    `EnvironmentFile=/etc/faas/sealed.env` shape is what every
    apid env var currently relies on. Not worth the refactor for
    apid alone — the threat model assumes apid is the trusted
    consumer of those secrets.
  - **Make sealed.env 0400 root-owned by a single dedicated uid that
    only apid joins.** Requires a new system user + group +
    `SupplementaryGroups=` plumbing on every apid unit version; the
    load-bearing property is unchanged (apid is still the only
    process that opens the file). Larger surface for the same
    security gain.
  - **Encrypt-at-rest the entire `secrets.env` via host.age.**
    Currently a 0440 root:faas file. Spec §11 line 1245 allows 0400
    plain-on-disk for control-plane secrets; the threat model
    assumes root can read everything. Encryption would also force
    every non-root daemon to run unseal-on-boot code paths, which
    currently only apid does (the apid host-age identity path).
    Deferred — opt-in only if a future audit demands it.
  - **Defer to a follow-on PR cluster** (PR-A / PR-B / PR-C). The
    split is mechanical (8 unit files + 1 ansible perm-assert +
    1 gate + 1 ADR + 1 spec line) and the runtime perm check +
    systemd's `%d/` resolution make the change load-bearingly safe.
    Splitting across PRs adds reviewer overhead without buying
    anything.

## Files

| File | Change |
|---|---|
| `pkg/daemonunitspec/{schedd,meterd,githubd,gatewayd_internal,vmmd,imaged,builderd}.go` | drop `EnvironmentFile=/etc/faas/sealed.env`; add per-daemon `LoadCredential=` + `Environment=KEY=%d/<id>` (for PATH-shaped env vars: `FAAS_HOST_AGE_IDENTITY_PATH` etc) or `EnvironmentFile=-/etc/faas/secrets/<daemon>/<daemon>.env` (for CONTENT-shaped env vars: session key, sealed blob, GitHub App creds, billing secrets, DNS token). Source of truth — `make generate` propagates to all 4 trees. |
| `deploy/systemd/*.service` + ansible mirrors + `deploy/controlplane/systemd/*.service` (v1 tombstone) | regenerated by `make generate` from the constructors above. |
| `deploy/ansible/roles/{schedd,meterd,githubd,gatewayd-internal,vmmd,pg-basebackup-push,tasks/main.yml` (new or appended to existing) | `copy:` + `mode: 0400` for each new secret file; mirror `host.age.previous` perm-assert pattern. |
| `scripts/ci/check_sealed_env_scope.sh` (NEW) | mirrors `check_systemd_hardening.sh` shape; walks 4 production trees + 6 ansible role files/ paths. |
| `Makefile` | `sealed-env-scope-check:` target appended to `lint:` recipe (Makefile:457). |
| `.github/workflows/ci.yml` | `sealed.env scope gate` step inside the `lint + build` job next to `systemd hardening gate`. |
| `docs/faas_implementation_spec.md` §11 line 1245 | amend to mention the `LoadCredential=` / `secrets.env` split + apid-only invariant. |

## Reused patterns

- `scripts/ci/check_systemd_hardening.sh` — exact template shape
  (88 LOC, `errs` counter, fail-loud stderr contract).
- `allowedSecretPerm` at `cmd/gatewayd-internal/secrets.go:44-50` —
  unchanged runtime check; the CI gate is its *static* superset.
- ansible `host.age.previous` perm-assert at
  `deploy/ansible/roles/control_plane_service/tasks/main.yml:323-334`
  — mirrored per new secret file.
- `make systemd-hardening-check` target at `Makefile:490` — exact
  recipe to mirror for `make sealed-env-scope-check`.
- `LoadCredential=<id>:<path>` + `Environment=KEY=%d/<id>` pattern
  for PATH-shaped env vars (already used by `UnitApid` for
  `FAAS_HOST_AGE_IDENTITY_PATH` /
  `FAAS_HOST_AGE_IDENTITY_PREVIOUS`). The same pattern is the
  canonical answer for any future PATH-shaped per-daemon secret.
- `EnvironmentFile=-/etc/faas/secrets/<daemon>/<daemon>.env`
  pattern for CONTENT-shaped env vars at 0400 root:root.
  systemd parses the file at activation and sets each `KEY=value`
  in the process env; downstream `os.Getenv` reads content directly.
  This is the canonical answer for any future CONTENT-shaped
  per-daemon secret.

## Out of scope (explicit deferrals)

- **Issue #603 (`github-app.pem` perm enforcement).** `cmd/githubd/main.go:469`
  `readKeyPEMDefault` does not perm-check the file. Defer to a follow-on
  PR that runs `os.Stat` → `allowedSecretPerm` before `os.ReadFile`,
  mirroring `cmd/gatewayd-internal/secrets.go:56`. This PR only changes
  how the file gets provisioned (mode 0400) + how the daemon reaches it
  (`LoadCredential`).
- **Encrypt-at-rest of `secrets.env`** — currently a 0440 root:faas file.
  Could be age-sealed via `host.age` like `host.age.previous` but only
  apid has the identity. Out of scope: spec §11 line 1245 allows 0400
  plain-on-disk for control-plane secrets; the threat model assumes root
  can read everything.
- **Moving `FAAS_HOST_KEY_PATH` from `/etc/faas` to `LoadCredential`.**
  `FAAS_HOST_KEY_PATH` is currently a *path* in env, not the key material
  itself. The daemon reads the path then `os.ReadFile`. Already fails
  closed if the file is missing. Skipping the `LoadCredential` conversion
  keeps this PR small.
- **Renaming `/etc/faas/sealed.env` → `/etc/faas/secrets/apid.env`.**
  Strictly cosmetic; `sealed.env` stays for apid to keep operator muscle
  memory.

## Verification

```bash
# Local pre-merge (must pass)
go build ./...                                          # no code changes — should be a no-op
go test ./cmd/... -count=1                              # 26 tests, no regressions
go test ./pkg/daemonunitspec/ -count=1                  # constructor tests
go test ./pkg/daemonunit/ -count=1                      # renderer round-trip tests
make generate                                           # idempotent — no diff after first run
make generate-check                                     # regenerated == committed
gofmt -l pkg/daemonunitspec/ deploy/systemd/ deploy/ansible/   # clean
shellcheck scripts/ci/check_sealed_env_scope.sh         # clean
make sealed-env-scope-check                             # gate trips if any non-apid unit has EnvironmentFile=/etc/faas/sealed.env
make lint                                               # includes the new gate
```

```bash
# CI gates (post-merge)
make sealed-env-scope-check                             # already in `make lint`
make systemd-hardening-check                            # regression
```

```bash
# Manual smoke (post-merge on a box)
systemctl show faas-schedd.service -p EnvironmentFiles -p LoadCredential
# expect: EnvironmentFiles=/etc/faas/compute-db.env (NOT sealed.env)
#         LoadCredential=faas_internal_svc_sealed_blob:/etc/faas/secrets/schedd/internal-svc.sealed
PID=$(systemctl show -p MainPID --value faas-schedd.service)
sudo tr '\0' '\n' < /proc/$PID/environ | grep -E 'STRIPE|GITHUB_CLIENT_SECRET|GOOGLE_CLIENT_SECRET|FAAS_PADDLE|FAAS_AUDIT_HMAC|FAAS_BILLING_PORTAL_URL|HETZNER_STORAGE_BOX'
# expect: zero hits (the tripwire test)
```

```bash
# Acceptance criterion 1 (per issue body — schedd must not load sealed.env)
sudo systemctl show faas-schedd.service -p EnvironmentFiles
# expect: EnvironmentFiles=/etc/faas/compute-db.env (only)

# Acceptance criterion 2 (per issue body — non-apid env vars must not leak)
sudo grep -E 'STRIPE_WEBHOOK_SECRET|GOOGLE_CLIENT_SECRET|GITHUB_CLIENT_SECRET' /proc/$PID/environ
# expect: empty
```

## References

- `docs/faas_implementation_spec.md` §11 line 1245 — invariant.
- `docs/faas_implementation_spec.md` §3 — ADR format.
- `deploy/systemd/faas-{apid,schedd,meterd,imaged,builderd,githubd,gatewayd-internal,vmmd,pg-basebackup-push}.service` — 9 unit files (the constructors in `pkg/daemonunitspec/<daemon>.go` are the source of truth).
- `pkg/daemonunitspec/registry.go:142` — `defaultTargets[0] = deploy/controlplane/systemd` (v1 cp-cp tombstone; `make generate` still emits into it).
- `deploy/ansible/roles/control_plane_service/tasks/main.yml:323-334` — perm-assert template.
- `scripts/ci/check_systemd_hardening.sh` — gate template (88 LOC).
- `cmd/gatewayd-internal/secrets.go:44-50` — `allowedSecretPerm` perm set (0o400/0o440/0o600/0o640).
- `Makefile:490` — `systemd-hardening-check` target template.
- `.github/workflows/ci.yml` — existing `lint + build` job shape.
- `cmd/apid/main.go:96-101` + `cmd/schedd/main.go:239-244` + `cmd/imaged/main.go:600-605` — byte-identical `envOr` helpers (no change needed; `os.Getenv` works for `LoadCredential`-resolved env vars too).
- Issue #585 (`enhancement`, `security`, `needs-ADR`).
- Roadmap: Mega-PR-C (issue #911 / ADR-110) cutover — `make generate` is the canonical path for every daemon-unit edit, no hand-edit allowed.