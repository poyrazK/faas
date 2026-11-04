# PolarBillingWebhooks

Alerts for Polar's webhook deliveries to apid (`/v1/webhooks/polar`) and the
subscription reconciliation that repairs missed ones (ADR-20261029094512,
ADR-20261029180000).

apid counts every answer it gives Polar in
`apid_ops_total{op="polar_webhook",code}`: `ok` (2xx), `rejected` (4xx: bad
signature or body) or `unavailable` (5xx: apid could not store or apply the
event; Polar retries). Polar disables an endpoint after ten consecutive
non-2xx deliveries and stops sending until an operator re-enables it.

Every 30 minutes one apid replica compares each Polar-billed account with its
customer's subscriptions and applies an event a missed delivery would have
carried once two passes agree (`apid_billing_reconcile_passes_total{result}`,
`apid_billing_reconcile_corrections_total{correction}`).

## Check

```sh
curl -fsS http://127.0.0.1:9101/metrics | grep -E '^apid_(ops_total\{op="polar_webhook"|billing_reconcile_)'
journalctl -u faas-apid --since '1 hour ago' --no-pager | grep -E 'polar_webhook|polar webhook|billing reconcile'
```

In the Polar dashboard (Settings, Webhooks), the endpoint's delivery log shows
each attempt's status and whether the endpoint is still enabled.

## FaasPolarWebhookFailing

Three or more deliveries in 30 minutes got a non-2xx answer.

1. `rejected` with `polar_webhook.verify_failed` in the log: the webhook
   secret in apid differs from the endpoint's secret in Polar, or apid's clock
   is off. Fix `FAAS_POLAR_WEBHOOK_SECRET` and restart apid.
2. `unavailable`: the log names the step (`invoice persistence`, `replay
   protection`, `state application`). Check Postgres first.
3. If Polar already disabled the endpoint, re-enable it in the dashboard and
   resend failed deliveries from its log. Reconciliation repairs plan and
   payment state on its own; invoices and refunds arrive only by webhook.

## FaasBillingReconcileFailing

Two reconciliation passes in two hours failed, so missed deliveries are not
repaired. The `billing reconcile` warnings carry the error; Polar API access
(token, rate limits) and Postgres are the usual causes.

## FaasBillingReconcileCorrected

Reconciliation applied an event a webhook should have delivered. The account
is now correct; the cause is not. Look for `FaasPolarWebhookFailing`, apid
restarts at the time, and the endpoint's delivery log in Polar.
