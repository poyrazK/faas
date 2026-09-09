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
# S3/R2/OVH only; omit for attached GCS ADC:
faas_object_storage_provider_env_src: /operator/secrets/object-storage.env
```

The provider environment file contains only the variables named by the JSON
registry, for example `OVH_S3_ACCESS_KEY=...`. It is installed root-only and
loaded by systemd into `apid` and `s3-gatewayd`. Switching to an ADC backend
and clearing `faas_object_storage_provider_env_src` removes any stale file.
Never put secret values in the JSON.

Before enabling the role, create a DNS-only Cloudflare `A`/`AAAA` record for
`s3.gregale.dev` pointing at the Caddy edge. Do not enable the Cloudflare proxy:
its request-size and duration ceilings would become accidental Gregale limits.
The runtime `s3_enabled` flag remains false until an operator explicitly flips
it after the live qualification and branded smoke test pass.
