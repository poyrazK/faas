# Crash snapshot and fork alerts

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_crash_snapshots`. ADRs: 732 (production forks), 733 (crash snapshots).

Crash captures are copies of production memory, end-user data included. The
DPA says they are encrypted at rest within one imaged pass (5 s) and kept in
plaintext only while a fork of them is open. Treat an encryption alert as a
compliance problem first.

| Alert | Severity | Metric |
|---|---|---|
| `FaasCrashCaptureEncryptionKeyMissing` | page | `imaged_crash_capture_encryption_key_missing` |
| `FaasCrashCaptureUnencrypted` | warn | `imaged_crash_capture_unencrypted_oldest_age_seconds` |
| `FaasCrashCaptureFileOpErrors` | warn | `imaged_crash_capture_ops_total{result="error"}` |
| `FaasCrashCaptureFailures` | warn | `schedd_crash_captures_total` |
| `FaasForkRestoreFailures` | warn | `schedd_fork_restores_total` |

Other signals: `schedd_crash_capture_duration_seconds` (how long a capture
takes, including the pause of the serving instance),
`schedd_fork_restore_duration_seconds`, `schedd_fork_claim_wait_seconds`
(for `source="crash_capture"` this includes imaged staging the plaintext), and
`gatewayd_crash_capture_requests_total` (5xx triggers: `requested`, `refused`,
`error`; `refused` is normal for apps without opt-in or within the cooldown).

## FaasCrashCaptureEncryptionKeyMissing

imaged has captures waiting and no host age identity, so it encrypts nothing.

```bash
systemctl show imaged -p Environment | tr ' ' '\n' | grep FAAS_HOST_AGE_IDENTITY_PATH
sudo ls -l "$(systemctl show imaged -p Environment | tr ' ' '\n' | sed -n 's/^FAAS_HOST_AGE_IDENTITY_PATH=//p')"
journalctl -u imaged --since -30m | grep 'no host age identity'
```

Restore the identity file the control plane's other daemons use (the same
one sealing env secrets), then restart imaged. The next pass encrypts the
backlog. If the key cannot be restored quickly, turn off new captures
(`FAAS_CRASH_SNAPSHOTS=0` on gatewayd-internal and schedd) so the backlog
stops growing.

## FaasCrashCaptureUnencrypted

A ready capture has been in plaintext for more than 2 minutes with a key
present. Encryption is failing or imaged's crash pass is not running.

```bash
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=%7B__name__%3D~%22imaged_crash_capture.*%22%7D'
journalctl -u imaged --since -30m | grep 'crash capture'
psql -c "select id, plaintext_state, captured_at from crash_captures where status='ready' and plaintext_state='present' order by captured_at"
```

Common causes: a full storage disk (the encrypted twin cannot be written), a
missing memory or vmstate object (the capture was half written; it fails
encryption every pass until it expires), or imaged stuck elsewhere.

## FaasCrashCaptureFileOpErrors

The `op` label says which step fails:

- `purge`: plaintext stays on disk. Check permissions and disk errors under
  the capture directory; the row stays `purging` and is retried every pass.
- `stage`: a fork waits queued. Usually the encrypted object is missing or
  the sealed key does not open with the current host identity (key rotated
  without keeping the old one).
- `expire`: captures outlive their 7-day retention. The row stays `ready`
  until the delete succeeds.
- `encrypt`: see `FaasCrashCaptureUnencrypted`.

## FaasCrashCaptureFailures

More than half of the captures for a trigger failed or timed out in the last
hour. A failed capture destroys the instance it paused (same posture as warm
snapshots), so customers also see extra cold starts.

```bash
journalctl -u schedd --since -1h | grep 'crash capture'
journalctl -u vmmd --since -1h | grep -i snapshot
psql -c "select failure_code, count(*) from crash_captures where requested_at > now() - interval '1 hour' group by 1"
```

`storage_unsupported` is not part of this alert: it is the expected refusal
on GCS or OCI storage.

## FaasForkRestoreFailures

More than half of the fork restores for a source failed with
`restore_failed` in the last hour. Customer-side refusals (`no_capture`,
`no_capacity`, `account_inactive`, `deployment_unavailable`) are excluded.

```bash
journalctl -u schedd --since -1h | grep 'fork coordinator'
journalctl -u vmmd --since -1h | grep -iE 'restore|quarantine'
```

For `source="crash_capture"`, also check that the capture was staged
(`plaintext_state='staged'`) and its Firecracker version matches the node's.
