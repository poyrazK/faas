# Authorizing a Data API

The serving login has restricted PostgreSQL grants; row-level security decides
which rows a user may read or change. Configure the issuer, JWKS and audience
when creating the API, and use verified JWT claims in database policies.

1. Define application tables and RLS in numbered migrations.
2. Exercise both allowed and denied reads and writes for two actual subjects.
3. For tenant policies, also test the same subject with a different tenant claim.
4. Replay the scenarios after policy, membership or function-body changes.

Generated type compatibility does not prove policy or function-body compatibility.
Use the [local inspector's guided checks](../data-api.md#guided-rls-checks-from-a-saved-relationship-view)
and [hosted authorization replay](../data-api.md#replay-authorization-scenarios-against-a-hosted-api).

Anonymous access requires an explicit public-read or public-RPC allowlist.
Server keys stay in trusted backends and can have narrower permissions. Do not
put server keys or migration credentials in a browser.

Live reads share these rules. Access reports resynchronize live endpoints, and
anonymous/server-key leases renew every 30 seconds. A changed access fingerprint
closes affected subscriptions before another result is forwarded. This is not
a global instantaneous revocation guarantee: old nodes and delayed endpoint
propagation require rollout and operational verification.

See the [RLS reference](../data-api.md#row-level-security-is-required),
[server-key permissions](../data-api.md#restrict-what-a-server-key-can-do), and
[authorization lifecycle checks](../data-api.md#authorization-lifecycle-checkpoints).
