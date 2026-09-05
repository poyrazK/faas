# GitHub webhook secret rotation

GitHub signs every delivery for a GitHub App with one App-level webhook
secret. The same `FAAS_GITHUB_WEBHOOK_SECRET` must therefore be present in
both `gatewayd-internal` and `githubd`; it is not scoped per customer or
installation.

## Rotate

1. Generate a 32-byte random secret and stage it in both daemon secret files:
   `/etc/faas/secrets/gatewayd-internal/gatewayd-internal.env` and
   `/etc/faas/secrets/githubd/githubd.env`.
2. Set the same value in the GitHub App's webhook settings.
3. Restart both daemons together.
4. Use the GitHub App delivery log to redeliver a recent `ping`, `push`, or
   `pull_request` event and confirm a `2xx` response.

GitHub exposes only one active webhook secret. Coordinate steps 2 and 3 in a
short maintenance window; deliveries rejected during the switch remain in
GitHub's delivery log and can be redelivered after both daemons are healthy.

## Verify

- `gatewayd-internal` must accept the HMAC before proxying the request.
- `githubd` must accept the same HMAC, durably insert the delivery, and return
  `202 Accepted`.
- A duplicate redelivery with the same `X-GitHub-Delivery` value must return
  `202` without creating duplicate builds.
- The delivery should advance from `pending` to `succeeded` in
  `github_webhook_deliveries`; repeated processing failures eventually move it
  to `dead` with `last_error` populated.

The installation-scoped secret table and admin command are retained only for
legacy non-GitHub senders that add an explicit installation header. Do not use
them to rotate the GitHub App's normal webhook secret.
