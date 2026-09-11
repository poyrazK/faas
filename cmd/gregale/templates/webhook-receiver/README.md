# webhook-receiver

A minimal port-8080 Node.js HTTP endpoint that accepts inbound
webhooks from any provider — Stripe, GitHub, generic CRMs, your
own backend, etc. The scaffold is provider-agnostic on purpose:
most webhook providers sign their payloads differently, so the
template ships ONE auth mechanism (a shared `X-Webhook-Secret`
header) and lets you bolt provider-specific HMAC checks on top.

This is a SCAFFOLD, not a production receiver — it echoes back a
preview of every accepted request so the customer's first smoke
test can confirm what arrived without writing custom logging.

## Auth

When `WEBHOOK_SECRET` is set, every POST must include a matching
`X-Webhook-Secret: <secret>` header. The compare is constant-time
so timing attacks against the secret length don't work.

When `WEBHOOK_SECRET` is unset, every POST is accepted. Useful for
development or for providers that don't sign their payloads.

## Path allowlist

`WEBHOOK_ALLOWED_PATHS` is an optional comma-separated list that
scopes the receiver to specific paths. Two shapes per entry:

- `"/stripe"` — exact match only
- `"/stripe/*"` — exact match OR any sub-path

Defaults to `"/*"` (accept any path) when unset. Useful examples:

```sh
# Stripe + GitHub only, nothing else
WEBHOOK_ALLOWED_PATHS=/stripe,/github
WEBHOOK_ALLOWED_PATHS=/stripe/*,/github/*,/internal/*
```

## First deploy with a secret

Create the file outside this directory, restrict it to your user, and
deploy. Gregale creates the app, seals the secret, and only then starts
the runtime:

```sh
printf 'WEBHOOK_SECRET=%s\n' "$(openssl rand -hex 32)" > ../webhook-receiver.secrets
# Optional: printf 'WEBHOOK_ALLOWED_PATHS=/stripe,/github\n' >> ../webhook-receiver.secrets
chmod 600 ../webhook-receiver.secrets
gregale deploy --secrets-file ../webhook-receiver.secrets
```

## Rotate or update secrets

```sh
gregale secrets set --app <slug> WEBHOOK_SECRET=$(openssl rand -hex 32)
# optional: pin the receiver to specific paths
gregale secrets set --app <slug> WEBHOOK_ALLOWED_PATHS=/stripe,/github
```

If `WEBHOOK_SECRET` is missing, the receiver still works in
"accept anything" mode. Don't ship that to production.

## Deploy

From this directory:

```sh
gregale deploy
```

## Try it

```sh
# Health probe — shows whether the secret is configured and what
# paths are allowed.
curl https://<slug>.gregale.dev/healthz

# Unauthenticated POST when WEBHOOK_SECRET is unset:
curl -X POST -H 'content-type: application/json' \
     -d '{"event":"test","data":{"hello":"world"}}' \
     https://<slug>.gregale.dev/stripe

# Authenticated POST when WEBHOOK_SECRET is set:
curl -X POST \
     -H 'X-Webhook-Secret: <the-secret>' \
     -H 'content-type: application/json' \
     -d '{"event":"test","data":{"hello":"world"}}' \
     https://<slug>.gregale.dev/stripe
```

## Adding provider-specific verification

Drop a HMAC verifier before the `console.log` in `handler.js`.
Stripe example:

```js
import Stripe from "stripe";
const stripe = new Stripe(process.env.STRIPE_WEBHOOK_SECRET);

// inside the handler, before the echo:
const sig = req.get("Stripe-Signature");
let event;
try {
  event = stripe.webhooks.constructEvent(req.body, sig, process.env.STRIPE_WEBHOOK_SECRET);
} catch (err) {
  return res.status(400).json({ ok: false, error: "invalid stripe signature" });
}
```

The `req.body` is a Buffer at that point because we set
`express.raw` upstream — the Stripe SDK expects the raw bytes for
its signature check.

## Re-deploy after edits

Edit `handler.js`, then `gregale deploy` from this directory.
