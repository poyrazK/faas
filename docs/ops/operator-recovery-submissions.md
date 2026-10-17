# Operator recovery submissions

Restart, park and cold-boot requests use a durable request key (ADR-1524).
Choose one key for one action. Reuse it with the same command, target and reason
if a reply is lost, the API restarts or the command times out. A retry returns
the original intent and acceptance response even after the instance stops or a
new deployment replaces the cold-boot target. Receipt retention has no TTL.

```sh
gregalectl instances force-restart --instance-id=INSTANCE_UUID --yes \
  --reason=incident_42 --request-key=incident_42_restart_1

# Once the intent ID is known, resume observation without another submission.
gregalectl instances wait --intent-id=INTENT_UUID --timeout=2m
```

The CLI generates a key when omitted and prints it to stderr before sending.
Successful JSON output includes request_key, operation and outcome; wait JSON
is the observed intent. Save the key and original arguments if the response
may be interrupted. --request-key applies to the authenticated API path;
break-glass calls have no submission receipt.

HTTP clients must send one Idempotency-Key containing 1–255 visible ASCII
characters and an empty body. Only confirm=true and an optional reason are
accepted, once each. Omitted/empty reason selects the documented action default;
instance UUID spellings are canonicalized. The actor account scopes the key
across the three recovery routes. Changing the action, target or reason returns
409 operator_submission_conflict. Reauthenticate or refresh step-up as needed;
every replay runs the current MFA, scope, allowlist, step-up and origin checks.
The new trace ID does not replace the original intent/audit attribution.

Acceptance atomically records a pending intent, the exact response bytes and an
audit outbox entry. A database error before commit leaves none of them. A lost
commit acknowledgement is recovered by the same request key. A failed notify
does not revoke acceptance: schedd's existing safety tick discovers the intent.
Audit delivery remains asynchronous; retries do not recreate a pruned handoff.
expires_at in the 202 response is advisory polling guidance, not receipt expiry.

Polling pending/running work is safe. A failed or cancelled intent remains the
result for its key. outcome_unknown requires the
[restart reconciliation procedure](operator-restart-recovery.md) before a new
action. A new key requests another action; it does not resume the first one.

## Upgrade

1. Pause the three recovery POST routes, drain old apid processes and in-flight
   submissions, and reconcile uncertain pre-upgrade requests against existing
   operator intents and audit history. Existing intent polling remains usable.
2. Apply migration 20261017110000001_operator_recovery_submissions.sql and start
   upgraded apid processes before reopening submissions. All recovery writers
   must use the atomic store seam. Mixed old/new apid writers are unsupported.
   Scheduler/vmmd protocols are unchanged by this migration; the separate
   [restart](operator-restart-recovery.md) and
   [cold-boot](operator-cold-boot-recovery.md) deployment requirements still apply.
3. Verify one accepted request, an identical replay through a replacement API,
   one intent and one attributed request audit. Verify changed parameters return
   409 and stale step-up cannot replay. Keep the returned intent/key for review.

An extant legacy cache row or reservation for an actor/key on any recovery route
returns 409 operator_submission_legacy, including expired rows. Its original
parameters and atomic acceptance cannot be proved. Inspect its intent before
choosing a new key. A pre-upgrade crash may have lost the cache write entirely;
those old requests cannot be recovered by this new protocol. Reconcile them
during the pause rather than retrying them as fresh submissions.

Retained submission rows prevent intent deletion. There is no automatic receipt
pruner. Downgrade refuses while any receipts exist; use a forward fix to retain
the replay boundary. Outbox delivery metadata can still be pruned normally.
