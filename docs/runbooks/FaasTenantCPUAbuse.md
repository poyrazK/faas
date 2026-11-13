# FaasTenantCPUAbuse

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, ADR-20261112120603.

Metric: `schedd_cpu_abuse_signals_total{plan, action}`. Severity `warn`, family `tenant_abuse`.

## Symptom

An instance stayed at 90% or more of its CPU quota for 15 minutes. The `action` label says what schedd did:

| action | plan | meaning |
|---|---|---|
| `recycled` | free | enforce mode: the instance was destroyed, its warm snapshot marked stale, and the recycle counted toward the account abuse hold |
| `shadow` | free | shadow mode: recorded only; the instance kept running |
| `flagged` | hobby, pro, scale | recorded only, in every mode |

## Verify

Each signal is audited as `instance.cpu_abuse` with the app as its subject. The event data names the instance, account, plan, action, mode, used and quota millicores, and sustained seconds.

```sql
SELECT at, subject AS app, data->>'account' AS account, data->>'plan' AS plan, data->>'action' AS action,
       data->>'used_millicores' AS used_m, data->>'quota_millicores' AS quota_m
FROM events WHERE kind = 'instance.cpu_abuse' AND at > now() - interval '24 hours' ORDER BY at DESC;
```

schedd also logs `cpu abuse: sustained CPU` with the same fields. A recycled instance has a STOPPED transition of kind `cpu_sustained`.

## Triage

1. Look at the app's image and deploy history: the ADR-368 `deployment.abuse_scan` event, any `FaasImageAbuseFlagged` or `FaasGuestDNSBlocked` alerts for the same app, and the egress flow log (ADR-371) for long-lived 443 flows to a single destination.
2. Check the request pattern: a Free app pinned at its quota while receiving a steady trickle of requests from one source (often itself or one IP) is the miner keep-alive shape.
3. Legitimate signs: varied clients, request rate that follows a daily pattern, a CPU-heavy framework (image processing, PDF rendering) visible in the code.

## Act

- Abuse: place a hold by hand if schedd has not already (`gregale admin abuse-hold place --note "<why>" <account_uuid>`); see `FaasTenantEgressAbuse.md` for the hold lifecycle.
- Legitimate Free app: nothing to do in shadow mode; in enforce mode, release any hold (`gregale admin abuse-hold release --note "<review outcome>" <account_uuid>`) and suggest a paid plan.
- Paid `flagged`: review only; paid plans are never recycled by this rule.

## Rollout

The switch is `cpu_abuse_mode` in schedd.toml or `FAAS_CPU_ABUSE_MODE` in an environment drop-in (`off`, `shadow`, `enforce`; default `shadow`). Move to `enforce` after a week of `shadow` signals has been reviewed and the Free false-positive rate is acceptable.
