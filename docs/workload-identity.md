# Workload identity tokens

Gregale app VMs expose a loopback-only token endpoint:

```text
GET $FAAS_WORKLOAD_IDENTITY_ENDPOINT?audience=sts.amazonaws.com
```

`guest-init` forwards the request over the VM's AF_VSOCK channel. vmmd
resolves the stream to its instance and mints a five-minute RS256 JWT; no API
key or provider secret is copied into the image. The instance may still be
booting: an app's process runs before readiness (a worker for the whole
characterization window), so it can take its token, and its first queue
lease, without waiting to become RUNNING. The endpoint returns the standard
OAuth shape:

```json
{"access_token":"…","token_type":"Bearer","expires_in":300}
```

The assertion has issuer `FAAS_WORKLOAD_IDENTITY_ISSUER` (default
`https://identity.gregale.dev`), subject `app:<app_id>`, and the claims
`account_id`, `app_id`, and `instance_id`. The requested audience is included
in `aud`, so the same app can use separate trust policies for AWS, GCP, and
Cloudflare.

To enable signing on a vmmd node, provision the same RSA-2048+ private key on
the node and set these `vmmd.toml` fields (the `FAAS_WORKLOAD_IDENTITY_*`
environment variables override them):

```text
FAAS_WORKLOAD_IDENTITY_KEY_PATH=/etc/faas/secrets/workload-identity.key
FAAS_WORKLOAD_IDENTITY_ISSUER=https://identity.example.com
FAAS_WORKLOAD_IDENTITY_KEY_ID=gregale-workload-identity-1
```

The equivalent TOML keys are `workload_identity_key_path`,
`workload_identity_issuer`, `workload_identity_key_id`, and
`workload_identity_ttl` (for example, `"5m"`).

### Provision with Ansible (ADR-1141)

Generate the cluster key once on the operator workstation and let the roles
install it:

```text
gregalectl workload-identity init --out ~/gregale-secrets/workload-identity
# inventory / host_vars:
faas_workload_identity_source_dir: ~/gregale-secrets/workload-identity
```

Run `bootstrap-control-plane` first: it installs the public JWKS at
`/etc/faas/workload-identity.jwks.json`, which apid uses for queue leases and,
when their own settings are empty, Flags and Operations (`workload_jwks_path`
in apid.toml, or `FAAS_WORKLOAD_JWKS_PATH`). Then run `bootstrap-compute`: it
installs the key under `/etc/faas/secrets`, sets the key path and key id in a
vmmd drop-in and restarts vmmd. `gregalectl workload-identity rotate` writes a
new key and keeps the old public key in the JWKS; roll it out in the same
order. `vmmd_workload_identity_tokens_total{result="not_configured"}` and the
`FaasWorkloadIdentityNotConfigured` alert show apps asking a node without a
key.

The vmmd metrics listener serves the matching public JWKS at
`/.well-known/jwks.json` when a signer is configured. Publish that path at the
configured issuer URL and register the issuer/audience pair with the cloud
provider. Rotate by installing a new key and key id and keeping the previous
public key available in the issuer's JWKS for at least the token TTL before
retiring it.

The endpoint is unavailable while the key is unset (`identity_not_configured`)
and never falls back to a static credential.
