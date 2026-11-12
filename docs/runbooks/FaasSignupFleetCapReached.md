# FaasSignupFleetCapReached

The platform created `api.SignupAccountsPerDay` (2,000) accounts in the last
24 hours, so apid refuses every new sign-up with a 429 until the oldest of
them ages out (ADR-20261104145353; metric `apid_signups_capped_total{scope="fleet"}`,
ADR 20261110120000). Every new account carries a full Free allowance, so the
cap bounds what a sign-up farm can claim. Real customers are being turned
away while it is reached.

## Decide: farm or demand

1. Look at who signed up:

   ```bash
   sudo -u postgres psql faas -c "SELECT date_trunc('hour', at) AS hour, data->>'method' AS method, data->>'source' AS source, count(*) FROM events WHERE kind = 'auth.signup' AND at > now() - interval '24 hours' GROUP BY 1,2,3 ORDER BY 1"
   sudo -u postgres psql faas -c "SELECT split_part(email, '@', 2) AS domain, count(*) FROM accounts WHERE created_at > now() - interval '24 hours' GROUP BY 1 ORDER BY 2 DESC LIMIT 20"
   ```

2. Signs of a farm: one burst, a handful of throwaway mail domains, most
   accounts never verified (`email_verified_at IS NULL`), many
   `auth.signup_network_cap` refusals (`apid_signups_capped_total{scope="network"}`
   climbing too), apps deployed within minutes of sign-up.
3. Signs of real demand: a launch, a post or a partner integration; spread
   across domains and networks; most accounts verify.

## Act

- **Farm.** Leave the cap in place; it is doing its job. Suspend the farm's
  accounts in bulk with `gregalectl accounts suspend --reason signup_abuse`
  after confirming them, and block the networks at the edge if they keep
  coming. Never-verified accounts that hold nothing are deleted on their own
  after 60 days (`api.UnverifiedAccountDeleteAfter`).
- **Demand.** Raise `SignupAccountsPerDay` in `pkg/api/limits.go` through a
  normal PR and release; there is no runtime knob, on purpose. Note the
  reason in the PR.

The alert clears 15 minutes after the last refusal.
