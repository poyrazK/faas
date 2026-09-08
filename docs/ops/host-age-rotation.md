# Host age rotation

The X25519 host identity at `/etc/faas/secrets/host.age`
(mode `0400 root:root`, spec §11) seals every customer-visible secret
on the box: per-app `app_secrets` envelopes (apid writes),
per-instance TOTP MFA secrets (apid reads via
`LoadCredential=faas_host_age_identity`), per-wake secret env vars
(vmmd reads), githubd install tokens, alert evaluator webhook
secrets, and Gregale-issued S3 signing credentials. Every customer envelope is sealed to the host's
**current** age recipient; if the on-disk key changes without
plumbing to keep envelopes sealed under the **previous** key
unsealed, every MFA confirm + every wake secret injection +
every githubd install-token rehydrate 5xx's on the same instant.

This runbook is the rotation procedure. It is **not** a scheduled
cadence — host.age rotation is an incident-response or
policy-compliance event, not a 90-day recurring task. Issue #316 /
ADR-057.

## Threat model

The host identity is the root of the box's trust tree. Rotation
matters when:

1. **Identity compromise.** An attacker has read the on-disk
   `host.age` (0400 root:root implies root compromise — that's a
   Tier-0 event, but rotation is still the recovery step).
2. **Key age / policy.** Some compliance frameworks (SOC2 CC6.1,
   PCI-DSS 3.6.4) bound the on-disk age of cryptographic material.
   Gregale doesn't have a scheduled cadence, but if a
   future audit demands one, this is the procedure.
3. **Forward secrecy hygiene.** Even without compromise, rotating
   every N years caps the blast radius of any latent read event
   (e.g. a snapshot that was exfiltrated but never decrypted until
   later).

What rotation does NOT do by itself:

- It does NOT re-seal every pre-rotation envelope at the instant the
  key files are swapped. The optional apid walker re-seals
  `app_secrets` in the background. `s3-gatewayd` is the exception:
  after its restart in step 4 it synchronously re-seals all active
  Gregale S3 credentials before it begins listening.
- It does NOT change `audit-HMAC` values. The audit-join key is
  independently generated (`/var/lib/faas/audit-hmac.key`,
  0600 root:root — see `docs/ops/secrets-rotation.md`) and stable
  across host.age rotation. `events.data.email_hash` for the same
  email is identical before and after a host.age rotation.
- It does NOT change customer app-secret plaintext. Until the
  optional walker runs, existing `app_secrets` rows remain under
  their original recipient; the unseal-side identity set covers
  both keys during the overlap.

## v1 partial-deliverable

This runbook ships the 30-day overlap path. The full re-seal
flow (background daemon that re-seals every `app_secrets` row
from the previous key to the current key, gated by a config knob)
is filed as `issue-316-followup-rekey` and is **not** in this PR.
For a single-box deployment the overlap window is sufficient:
no operator-driven re-seal is required because every daemon
unseals under either key, and any new envelope written during
the overlap is sealed under the new key automatically.

ADR-089 (PR-C, 2026-08-10) ships the v2 follow-up: per-secret
on-demand rotation (`POST /v1/apps/{slug}/secrets/{key}/rotate`,
PR-B) plus a background re-seal walker (`FAAS_REKEY_ENABLED`,
PR-C) that drains the table without operator intervention. See
"Per-secret rotation" and "Background re-seal" below for the
operator-facing surfaces.

## Preconditions

- `gregale` is on PATH (it's the same binary as `faas`; the
  installer places it at `/usr/local/bin/gregale`).
- Operator has read access to `/etc/faas/secrets/` (root).
- Operator has `journalctl` access (root) to bounce daemons.
- Pre-rotation unseal errors are zero. Check before starting:

  ```sh
  # MFA unseal health (apid)
  psql -U faas -d faas -c \
    "select count(*) from audit_events where event_type = 'mfa.unseal_failed' and created_at > now() - interval '1 hour';"
  # expect: 0

  # Wake secret unseal health (vmmd)
  grep -c 'open age reader' /var/log/faas/vmmd.log | head -1
  # expect: 0 over the last hour

  # Alert dispatch unseal health (meterd)
  grep -c 'alertEvalSkippedDegradedTotal' /var/log/faas/meterd.log | head -1
  # expect: 0 — SkippedNoIdentity counter is the canary
  ```

  If any of these are non-zero, **do not rotate**. Investigate
  the unseal failure first — a rotation amplifies the failure
  across the entire box instead of a single tenant.

- The box is in a quiet window (no in-flight cron invocations,
  no build VM running). Rotation is not destructive, but the
  30-second bounce of five daemons + the 30-day envelope overlap
  is best done off-peak.

## Procedure

Six numbered steps. The expected wall-clock for a clean run is
under 5 minutes plus the five-daemon bounce.

### 1. Generate the new key + pre-flight check

```sh
sudo gregalectl host-age status
```

Confirm both `host.age` and `host.age.pub` exist with mode
`0400 root:root` and `0444 root:faas` respectively, and no
`host.age.previous` is present (the box is in pre-rotation
state).

```sh
sudo ls -la /etc/faas/secrets/
# expect:
# host.age       0400 root:root  …
# host.age.pub   0444 root:faas  …
# (no host.age.previous)
```

### 2. Rotate (atomically swap)

```sh
sudo gregalectl host-age rotate --commit
```

This performs three operations in one shell:

1. Reads current `/etc/faas/secrets/host.age`, parses the
   identity.
2. Atomic-rename `host.age` → `host.age.previous` (mode
   preserved).
3. Generates a new identity, writes it to `host.age` with
   mode `0400 root:root`.

The output is the new recipient string:

```
✓ Rotated host.age → host.age.previous; new current written.
  New recipient:                age1qz3p...
  Previous (now .previous):     age1abc...
  Next: chown root:root /etc/faas/secrets/host.age /etc/faas/secrets/host.age.previous && chmod 0400 both
  Next: systemctl restart faas-vmmd first (it owns host.age.pub), then faas-apid faas-meterd faas-githubd
  Next: gregalectl host-age status (verify all daemons on the new fingerprint after restart)
  Next: 30-day overlap window starts now; run 'gregalectl host-age prune-previous' after that
```

**The new recipient is what every NEW envelope will be sealed
to.** Record it in your team's secrets-rotation log
(format: `<date> <operator> host.age rotate <recipient-prefix>`)
so the rotation history is auditable.

### 3. Verify the on-disk shape

```sh
sudo ls -la /etc/faas/secrets/
# expect:
# host.age             0400 root:root  (new)
# host.age.previous    0400 root:root  (old)
# host.age.pub         0444 root:faas  (still the OLD recipient — see step 4)
```

```sh
sudo gregalectl host-age status
```

Output should show two fingerprints (current + previous), their
respective `mtime`, and a 30-day countdown annotation.

### 4. Bounce the daemons — vmmd FIRST, then the rest

The bounce order is load-bearing. **vmmd writes
`/etc/faas/secrets/host.age.pub` from its in-memory identity at
boot** (`pkg/secretbox/hostkey.go:172-181 WriteRecipientFile`); if
apid restarts first it reads the OLD recipient and seals new
envelopes against the OLD key — the daemons won't be able to
unseal them until vmmd comes up and rewrites the file.

```sh
# 1. vmmd FIRST — it picks up host.age, writes the new host.age.pub.
sudo systemctl restart faas-vmmd

# 2. apid SECOND — it reads the new host.age.pub as its sealing key.
sudo systemctl restart faas-apid

# 3. meterd + githubd — unseal-only; they read host.age via LoadHostKeys
#    and don't care which order they bounce in, so long as vmmd and
#    apid are already on the new identity.
sudo systemctl restart faas-meterd faas-githubd

# 4. s3-gatewayd — loads both identities and synchronously re-seals every
#    active S3 credential under the new current identity before listening.
sudo systemctl restart faas-s3-gatewayd
```

Daemons don't watch the file — systemd restart re-reads via
`LoadCredential=faas_host_age_identity` (apid) and the
`FAAS_HOST_AGE_IDENTITY_PATH` env var + LoadHostKeys(dir) for the
other four. Without the bounce, daemons still hold the
**pre-rotation** identity and the rotation does nothing.

After vmmd's restart, `host.age.pub` now points at the NEW
recipient:

```sh
sudo ls -la /etc/faas/secrets/
# expect:
# host.age.pub         0444 root:faas  (now the NEW recipient)
# host.age             0400 root:root  (new)
# host.age.previous    0400 root:root  (old)
```

Wait for the daemons to come up clean. A failed `faas-s3-gatewayd` start is a
prune blocker: inspect the log, revoke or repair the unreadable credential, and
restart successfully before removing `host.age.previous`.

```sh
sudo systemctl status faas-apid faas-vmmd faas-meterd faas-githubd faas-s3-gatewayd
# expect: active (running) on all five
```

### 5. Verify unseal health post-bounce

```sh
sudo journalctl -u faas-apid --since "5 min ago" | grep -i 'open_failed\|mfa.unseal'
# expect: zero matches

sudo journalctl -u faas-vmmd --since "5 min ago" | grep -i 'open age reader'
# expect: zero matches
```

Then exercise a representative unseal:

```sh
# Pick a customer app that has sealed env vars and confirm
# the read path still works (pre-rotation envelopes should
# unseal via the .previous identity; post-rotation envelopes
# via the current identity).
curl -sS -H "Authorization: Bearer $FAAS_ADMIN_TOKEN" \
    "https://api.gregale.dev/v1/apps/$APP_ID/secrets/$KEY_NAME"
# expect: 200 with the plaintext value
```

### 6. Stamp the rotation in the runbook log

```sh
echo "$(date -Iseconds) <operator> host.age rotate — committed, daemons bounced" \
  | sudo tee -a /var/log/faas/rotation.log
```

## Validation matrix

A rotation is healthy when ALL of the following are true:

| Signal | Source | Healthy value |
|---|---|---|
| `gregalectl host-age status` shows both fingerprints | operator CLI | current + previous visible |
| All five daemon status lines green | `systemctl is-active` | active (running) |
| `apid_open_failed_total` (Prometheus) | `/metrics` on apid:9090 | 0 |
| `vmmd_unseal_failed_total` | `/metrics` on vmmd:9090 | 0 |
| `alert_evaluator_skipped_total{reason="no_identity"}` | meterd `/metrics` | 0 |
| `githubd_unseal_failed_total` | githubd `/metrics` | 0 |
| Customer MFA confirm path returns 200 | manual curl | yes |
| Customer app-secrets GET returns 200 | manual curl | yes |
| Newly-sealed envelope sealed under new recipient | manual sqlc query | yes (recipient matches current) |
| Every active S3 credential has the current recipient KID | `object_s3_credentials.kid` | zero mismatches |

The first row is operator-driven; the next six are telemetry
that the runbook's PostGres query (or `metric-schema` curl) can
verify. The last two are end-to-end probes that exercise both
the pre-rotation and post-rotation unseal paths.

## Rollback

Up until `gregalectl host-age prune-previous`, the previous key is
still on disk as `host.age.previous`. Rollback is:

```sh
# Stop all five daemons.
sudo systemctl stop faas-vmmd faas-apid faas-meterd faas-githubd faas-s3-gatewayd

# Restore the previous key as current while retaining the rotated key as
# previous. Do not overwrite either identity: envelopes may exist under both.
sudo mv /etc/faas/secrets/host.age /etc/faas/secrets/host.age.rotated
sudo mv /etc/faas/secrets/host.age.previous /etc/faas/secrets/host.age
sudo mv /etc/faas/secrets/host.age.rotated /etc/faas/secrets/host.age.previous
sudo chmod 0400 /etc/faas/secrets/host.age /etc/faas/secrets/host.age.previous

# Restart in the same order as step 4: vmmd first (it owns
# host.age.pub), then apid (it reads host.age.pub as its sealing
# key), then meterd + githubd, then s3-gatewayd. The gateway re-seals active
# S3 credentials back under the restored current key before listening.
sudo systemctl start faas-vmmd
sudo systemctl start faas-apid
sudo systemctl start faas-meterd faas-githubd
sudo systemctl start faas-s3-gatewayd
```

This restores the pre-rotation identity as current while retaining
the rotated identity for the overlap. Every envelope remains
readable, including envelopes sealed between rotation and rollback.
Do not remove the now-previous rotated key until the app-secret
walker has drained and `faas-s3-gatewayd` has restarted
successfully. **Do not roll back after
`prune-previous`** — the previous file is gone and the rotation
is irreversible until you re-provision a fresh host.age from
backup.

The `gregalectl host-age rotate --abort` flag (proposed, not yet
shipped) takes an internal snapshot at rotate-time and restores
from it without the manual steps above. Filed as a follow-up
for v2.

## Escalation

Page `faas-platform-oncall` if **any** of the following holds
for >1 hour after a rotation:

- `alert_evaluator_skipped_total{reason="no_identity"}` > 0
  (canonical canary — the alert evaluator's SkippedNoIdentity
  counter is the single signal that "the unseal side is broken,"
  not the `alert_evaluator_enabled` gauge which only tracks
  "the evaluator is wired.")
- Any customer's MFA confirm endpoint returns 5xx for >1% of
  requests in a 5-minute window.
- A `systemctl status faas-*` line shows `failed` or
  `activating (auto-restart)` for >10 cycles.

Escalation path is documented in
[`docs/ops/escalation.md`](escalation.md) — Tier 0 is a manual
host.age replacement from backup + `prune-previous --force`,
after which the operator must coordinate with every customer
who had MFA enrolled during the rotation window to re-enroll.

## Acceptance

A rotation is considered "complete" when:

1. All entries in the validation matrix are green for 30
   consecutive days after step 4 of the procedure.
2. `gregalectl host-age prune-previous` runs cleanly (defaults:
   refuses if the previous file is <30 days old).
3. The runbook log (`/var/log/faas/rotation.log`) carries the
   timestamp, operator, and recipient prefix.

Only after step 1 has held for 30 days should the operator run
`gregalectl host-age prune-previous`. The default 30-day overlap is
the actual security primitive — shortening it requires
operator sign-off and a written justification in
`/var/log/faas/rotation.log`.

## Pruning the previous key (30+ days post-rotation)

```sh
sudo gregalectl host-age status
# confirm: host.age.previous age = 30+ days
sudo gregalectl host-age prune-previous
# default: refuses if previous <30 days
# --force: skip the age check
# --promote: rename previous → current instead of removing
```

`--promote` is the manual escape hatch for "the current key was
broken-on-arrival; let me use the previous one as the new
current." It refuses if `host.age` already exists; the operator
must `sudo rm /etc/faas/secrets/host.age` first to use this
escape hatch. The default flow is `prune-previous` (just
delete), which is irreversible.

## Per-secret rotation (on demand)

For customers who want a fresh envelope under the current
identity without waiting for the natural overlap (e.g. after
`prune-previous` deletes the previous key and a row's
ciphertext is now unreadable), `POST
/v1/apps/{slug}/secrets/{key}/rotate` re-seals one row in
place. The endpoint is RBAC-equivalent to the existing secrets
PUT (admin scope, MFA-gated, owner-or-admin); the audit kind
distinguishes it from first-time sets (`secret.rotated` vs
`secret.set`).

CLI shape (issue / ADR-089 PR-B):

```sh
faas secrets rotate --app my-app STRIPE_KEY \
  --value 'sk_live_...'
# or pipe from stdin / vault
faas secrets rotate --app my-app STRIPE_KEY < new-key.txt
```

Response:

```json
{
  "key": "STRIPE_KEY",
  "rotated_at": "2026-08-10T13:42:01.123456Z",
  "kid": "age1qz3p..."
}
```

The `kid` is the recipient fingerprint of the current host
identity (matches what `gregalectl host-age status` prints as
the current key). After rotate, GET against the row returns
the new ciphertext (still opaque to the customer); the
plaintext is held by vmmd's per-wake secret injection.

Idempotency: rotating the same value twice is allowed; each
call emits a fresh envelope under the current kid and a
`secret.rotated` audit row. The OpenMulti unseal side keeps
working through the 30-day overlap window regardless of how
many times the row has been rotated.

When to use:

- A row was sealed under the previous identity AND the
  operator has just run `prune-previous` (so OpenMulti no
  longer covers it). Per-secret rotate is the only path that
  recovers that row without rebuilding it from the customer's
  source-of-truth credential store.
- Compliance regimes that demand fresh envelopes on a
  per-secret schedule (e.g. quarterly rotation of every
  customer-managed secret).

When NOT to use:

- For the bulk "every row in the table is stale" case. Use the
  background re-seal (next section) — running per-secret
  rotate for every customer is a 1×N round-trip with no
  observable benefit.

## Background re-seal (after host identity rotation)

For the operator who has just promoted a new host identity and
wants every `app_secrets` row re-sealed without manual
intervention, `FAAS_REKEY_ENABLED=true` starts a background
walker in `apid`. The walker drains the table under the current
identity only — no operator round-trip, no per-customer API
call — and is crash-safe across daemon restarts.

### Enable the walker

1. Confirm the new identity is in production:

   ```sh
   sudo gregalectl host-age status
   # confirm: host.age exists, host.age.previous either does
   # not exist (clean state) OR is <30 days old (overlap
   # still active).
   ```

2. Stamp the env var on the `apid` systemd unit:

   ```sh
   sudo systemctl edit faas-apid
   # add:
   # [Service]
   # Environment="FAAS_REKEY_ENABLED=true"
   # Environment="FAAS_REKEY_PROGRESS_FILE=/var/lib/faas/rekey-progress.json"
   sudo systemctl restart faas-apid
   ```

3. Verify boot:

   ```sh
   sudo journalctl -u faas-apid -n 50 --no-pager | grep rekey
   # expect: "background re-seal enabled" + "progress_path=/var/lib/faas/rekey-progress.json"
   ```

### Monitor progress

The walker persists a JSON snapshot to
`FAAS_REKEY_PROGRESS_FILE` after every batch (default
`/var/lib/faas/rekey-progress.json`, mode `0o600 root:root`).
The on-disk file is the operator's source of truth — it is
crash-safe (atomic rename after every batch tick) and survives
a `systemctl restart`.

```sh
sudo cat /var/lib/faas/rekey-progress.json | jq
# {
#   "total": 50000,
#   "rekeyed": 42318,
#   "skipped": 7682,
#   "failed": 0,
#   "last_id": "<account_id>|<app_id>|<key>"
# }
```

`total` is cumulative rows visited; `rekeyed + skipped` is
the success metric. `skipped` is a row whose kid already
matched the current identity — natural PUTs during the
overlap window sealed under the new key, so the walker has
nothing to do for them. `failed > 0` is the tripwire; see the
next subsection.

The `GET /v1/admin/secrets/rekey-progress` endpoint returns
the same shape via HTTP. The endpoint is admin-only (admin
scope + the operator email allowlist from `FAAS_ADMIN_EMAILS`).
When `FAAS_REKEY_ENABLED` is unset the endpoint returns
`503 rekey_disabled` so a misconfigured box is observable on
the wire rather than silently returning a zero-progress 200.

```sh
curl -H "Authorization: Bearer $ADMIN_KEY" \
  https://apid.example.com/v1/admin/secrets/rekey-progress
```

### Failure recovery

`failed > 0` means a row's ciphertext could not be unsealed
(most often: the row was sealed under an identity that is no
longer in `OpenMulti` reach — the operator ran
`prune-previous` before the walker drained). The walker's
crash-safe model retries these on the next daemon restart; the
operator's recovery procedure is:

1. Stop apid.
2. Restore the missing identity: `gregalectl host-age rotate
   --previous <path-to-backup>` re-installs the previous key
   under `/etc/faas/secrets/host.age.previous`, restoring the
   OpenMulti overlap window.
3. Restart apid with `FAAS_REKEY_ENABLED=true`. The walker
   resumes from the on-disk cursor (the pinned cursor model
   in `pkg/rekey.Run` re-fetches failed rows via the `>=`
   fence, so the failed count drops to zero on retry).
4. Confirm:

   ```sh
   sudo cat /var/lib/faas/rekey-progress.json | jq .failed
   # expect: 0
   ```

If the previous identity cannot be recovered (the file is
truly lost), the only path is per-secret rotate for each
failed row from the customer's source-of-truth credential
store — the walker has nothing to unseal with.

### What the walker does NOT do

- It does not touch `audit-hmac.key`, `recovery-hmac.key`, or
  any other derived secret. The audit-join key is
  independently generated and stable across host.age rotation
  (spec §11).
- It does not change `host.age` or `host.age.previous`. The
  operator rotates the key file via `gregalectl host-age rotate`;
  the walker only re-seals `app_secrets` rows under the
  already-promoted new identity.
- It does not re-seal Gregale S3 credentials. `s3-gatewayd` owns
  those rows and synchronously re-seals all active credentials on
  startup; this is independent of `FAAS_REKEY_ENABLED`. A gateway
  startup failure therefore blocks pruning the previous identity.
- It does not retry a row whose kid already matched the
  current identity. `skipped` is a one-way decision: a row
  sealed under the current kid does not need to be re-sealed.
- It does not require SIGHUP or any signal handling. A new
  identity requires a daemon restart (the walker reads
  identities at boot via `secretbox.LoadHostKeys(dir)`).

### Disable the walker

Set `FAAS_REKEY_ENABLED=false` (or unset) and restart `apid`.
The walker exits; the progress file remains on disk for
audit / re-enable purposes. There is no "pause" — the walker
is either running or not.

## References

- `docs/adr/057-host-age-rotation.md` — ADR capturing the v1
  partial-deliverable and v2 follow-up scope.
- `docs/adr/089-secret-rotation.md` — ADR capturing the v2
  per-secret rotation surface (PR-B) + background re-seal
  walker (PR-C). The "Per-secret rotation" and "Background
  re-seal" sections above document the operator-facing surface
  of PR-C.
- `docs/adr/089-pr-cluster-outline.md` — PR-A/PR-B/PR-C split
  + the cron-runs / box e2e coverage plan.
- `docs/ops/secrets-rotation.md` — the surrounding secrets
  rotation doc; the host.age entry there references this
  runbook.
- `docs/runbooks/multi-host-rollout.md` — Tier 1 Phase 6, which
  requires the rotation runbook to ship before production
  multi-host is enabled.
- `pkg/secretbox/hostkey.go` — the underlying
  `LoadHostKeys(dir)` / `OpenMulti` plumbing.
- `pkg/rekey/rekey.go` — the crash-safe walk primitive wrapped
  by `cmd/apid/rekey_runner.go::Runner` (PR-C).
- `cmd/gregale/commands_host_age.go` — the operator CLI.
