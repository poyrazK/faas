# Event workflow recipe

Add this manifest's workflow to an app that handles `/record-payment` and
`/send-receipt`. The first handler should return a JSON `receipt_id`. Handler
implementations are not included. This example requires Hobby or above and the
preview workflow runtime on both apid and schedd.

Deploy the manifest with your app, then preview and publish a sample:

```bash
gregale events preview billing.stripe invoice.paid \
  --data '{"invoice_id":"inv_123","amount":150}'
gregale events publish billing.stripe invoice.paid --id invoice-paid-inv_123 \
  --data '{"invoice_id":"inv_123","amount":150}'
gregale workflows list --app billing
```

Reuse the same source and ID when retrying a publish. The workflow receives the
full CloudEvents envelope; input templates select fields from its `data` object.
Make handler side effects idempotent because step execution can retry. See the
[event-driven guide](../../docs/event-driven.md) for retention and replay behavior.
