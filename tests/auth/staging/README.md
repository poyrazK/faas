# Application Auth staging canary

This opt-in gate is ADR-1190's native acceptance. It deploys disposable
fixtures through the normal Gregale CLI, remote builder, managed bindings,
release tasks and public HTTPS edge: an Auth app (`gregale auth create`), a
migration release that creates a row-level-security table keyed on
`gregale_auth.uid()` and `gregale_auth.session_active()`, and a Data API bound
with `--auth`. Run it only against a staging target whose native VM fleet and
managed PostgreSQL backend have passed their own qualification gates.

## Configure an isolated target

It uses the Data API canary's manifest and rules: `environment: staging`, the
API origin, the isolated account ID, region, native builder node allowlist and
app host suffix. Supply the account bearer as `FAAS_TOKEN` through the secure
environment.

```sh
export FAAS_AUTH_STAGING_CONFIG=/etc/faas/auth-staging.json
export FAAS_AUTH_EVIDENCE=/var/tmp/auth-canary/evidence.json
make auth-staging-canary
```

The account needs capacity for three apps, one managed database, PostgreSQL
egress and release tasks.

## Checks

Every deployment's build provenance must name an allowlisted native builder.
The gate then verifies, in order:

- the Auth app serves an ES256-only JWKS, and sign-up issues tokens signed by a
  published key with the expected issuer, audience, subject, session and
  600-second lifetime;
- Data API row-level security returns only the caller's rows and refuses a
  write on another user's behalf;
- a global sign-out makes the still-unexpired access token see no rows and
  write nothing, and refuses its refresh token;
- `gregale auth admin-key create` yields a key the Auth app accepts once it
  has restarted, its user list carries no metadata, and the public GoTrue
  admin path is closed;
- `gregale auth import` creates a user through the admin API;
- an admin ban stops the banned user's rows and password sign-in;
- after the Auth app parks, a password sign-in wakes it (public elapsed time
  is recorded, not a restore SLO), and the new session sees its rows;
- no Auth instance failed or ran above the app's plan RAM; the day's snapshot
  and layer bytes are recorded once the storage rollup has run.

Evidence is written atomically with mode 0600: check names, deployment, build
and instance identities, wake time, plan RAM and storage bytes. Passwords,
tokens, the admin key and user addresses stay in memory and never reach
evidence, argv or logs. Cleanup follows the Data API canary: bindings are
retired before apps and the database, only identities acknowledged by this run
are deleted, and unknown outcomes are left for operator reconciliation.

`make auth-staging-check` runs the harness against a simulated control plane
and edge. Portable results are not native acceptance evidence.
