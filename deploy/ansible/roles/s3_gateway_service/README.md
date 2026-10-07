# s3_gateway_service

Installs the optional `faas-s3-gatewayd` control-plane service and routes
`s3.gregale.dev` through the existing Caddy edge to its loopback data listener.
The customer protocol stays fixed at path-style SigV4 in `us-east-1`; the JSON
registry selects the interchangeable upstream provider.

The role is intentionally opt-in. Add these inventory variables and run the
normal control-plane bootstrap:

```yaml
faas_object_storage_gateway_managed: true
faas_object_storage_gateway_enabled: true
faas_object_storage_config_src: /operator/config/object-storage.json
faas_object_storage_gateway_edge_mode: proxied  # or direct for DNS-only TLS origin
# S3/R2/OVH only; omit for attached or impersonated GCS ADC:
faas_object_storage_provider_env_src: /operator/secrets/object-storage.env
```

The provider environment file contains only the variables named by the JSON
registry, for example `OVH_S3_ACCESS_KEY=...`. It is installed root-only and
loaded by systemd into `apid` and `s3-gatewayd`. Switching to an ADC backend
and clearing `faas_object_storage_provider_env_src` removes any stale file.
Never put secret values in the JSON.

Installing or changing the registry/provider environment also restarts
`faas-gatewayd-public`. That daemon reads the registry at boot; restarting it
here is required for public bucket mounts to become active when storage is
provisioned after the edge service. Its socket remains bound during the
restart. The proxied Caddy route pins `Accept-Encoding: identity` on the
origin hop because Cloudflare can rewrite that header before Caddy, which
would invalidate SigV4 signatures. The direct route preserves the signed header.

For `proxied`, use `deploy/object-storage.example.json` or the GCS example
and a proxied Cloudflare record for `s3.gregale.dev`. The registry declares
`transfer.profile: proxied` and caps single PUTs/parts at 64 MiB. Bypass cache,
URL normalization, redirects, response transforms and interactive challenges;
the CDN's duration ceilings still apply.

For larger requests, use `deploy/object-storage.direct.example.json`, set
`faas_object_storage_gateway_edge_mode: direct`, and configure DNS-only routing
to the Caddy TLS origin. This example allows 512 MiB single PUTs and 1 GiB parts, 5 TiB
multipart objects, a two-hour combined transfer budget, four uploads, a 2 GiB
aggregate single-PUT spool and a 1 GiB free-space reserve. Allocate sufficient
spool disk and account/provider budgets before enabling it. The role does not
change DNS. The branded hostname, SigV4 region and path/query bytes stay fixed.

Registry `transfer.profile` and the inventory edge mode must match. During an
upgrade, add the explicit profile to older registries: an omitted runtime
profile preserves old byte limits using direct semantics. The role defaults
to proxied and refuses a mismatched configuration before updating Caddy.
Caddy transport deadlines follow `transfer.timeout_seconds` plus the existing
five-second settlement allowance; provider request replay remains disabled.
The runtime `s3_enabled` flag remains false until an operator explicitly flips
it after deployment qualification and the branded smoke test pass. Local fixtures
qualify the implementation without contacting real storage providers.
