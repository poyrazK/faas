# FaasSecurityMailDropped

apid sends security mail (password reset, sign-in links, email verification,
email-change confirmations and account notices) off the request path through
a pool of `api.PasswordResetMailInFlightMax` (64) concurrent sends per apid.
When every slot is busy the mail is dropped, not queued, so the public answer
stays the same whether or not the account exists (bug hunt H86). The metric
`apid_background_mail_dropped_total` counts those drops (ADR 20261110120000).
A customer who asked for a reset or sign-in link got nothing.

## Find the cause

1. **The mail transport is slow.** Each send holds its slot until the mail
   server answers or `api.PasswordResetMailTimeout` passes. Check the internal
   mail service and the upstream provider:

   ```bash
   journalctl -u faas-apid --since '-30m' | grep -E 'mailer|email_change|password_reset|notice' | tail -50
   ```

   See [internal-mail](../ops/internal-mail.md) for the outbox and provider
   health checks.
2. **A burst of requests.** Forgot-password or sign-in-link floods fill the
   pool. Check the per-IP auth limiter and the `auth.mail_throttled` audit
   events; a flood from a few networks is abuse, not load.
3. **One apid only.** Compare the metric per instance. One instance dropping
   while others are idle points at that host's network path to the mail
   server.

## Act

- Fix the transport first; the pool drains on its own once sends finish.
- For a flood, block the sources at the edge.
- Customers whose mail was dropped can ask again; the per-account mail
  limits (one a minute, five an hour) still apply.

Raise `PasswordResetMailInFlightMax` only if sends are fast and the pool is
still full under legitimate load.
