# Workload identity tokens

Gregale app VMs expose a loopback-only token endpoint:

```text
GET $FAAS_WORKLOAD_IDENTITY_ENDPOINT?audience=sts.amazonaws.com
```

`guest-init` forwards the request over the VM's AF_VSOCK channel. vmmd
resolves the peer CID to the live instance and mints a five-minute RS256 JWT;
no API key or provider secret is copied into the image. The endpoint returns
the standard OAuth shape:

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

The vmmd metrics listener serves the matching public JWKS at
`/.well-known/jwks.json` when a signer is configured. Publish that path at the
configured issuer URL and register the issuer/audience pair with the cloud
provider. Rotate by installing a new key and key id and keeping the previous
public key available in the issuer's JWKS for at least the token TTL before
retiring it.

The endpoint is unavailable while the key is unset (`identity_not_configured`)
and never falls back to a static credential.
