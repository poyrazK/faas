# FaasResponseCache

Use this runbook when response-cache bypass, skipped-store, stale-serve, or L1
occupancy signals are elevated.

## Symptom

The response cache is unexpectedly bypassing requests, declining to store
responses, serving stale entries for a sustained period, or approaching its L1
memory ceiling. Redis connectivity warnings indicate that gateways have fallen
back to local-only caching; request handling remains available.

## Check

1. Split `gateway_response_cache_total` by `outcome`. High `bypass_authed` is
   usually a cache rule matching credentialed traffic; high `store_skipped`
   means the origin is returning a veto header/status or a body larger than 1
   MiB.
2. Treat sustained `stale_if_error_served` as an origin incident. A rise in
   `stale_while_revalidate_served` is expected when SWR is configured, but a
   continuously high ratio can indicate refresh failures.
3. If the local `gateway_response_cache_bytes` gauge stays above 80% of 64 MiB,
   reduce route or `vary_on` cardinality. Redis does not increase the L1 limit.

## Recover

4. For a suspected bad entry, run `gregale cache purge <slug>` or add
   `--path '/route/*'` for a scoped purge.
5. If the gateway logs `distributed response cache unavailable`, verify the
   Redis endpoint, credentials, TLS, and reachability. Requests remain
   available through L1/origin while Redis is down.

Unset `FAAS_GATEWAY_RESPONSE_CACHE_REDIS_URL` and restart
`gatewayd-internal` to roll back to local-only caching. Existing Redis entries
expire by TTL and are not authoritative.
