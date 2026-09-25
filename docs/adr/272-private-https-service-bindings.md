# ADR-272: Opt-in private HTTPS for service bindings

- **Status:** accepted
- **Date:** 2026-09-25
- **Decision:** Add a separate TLS 1.3 listener on the node's tenant bridge port 443 for `<service>.internal`. It is disabled unless gatewayd-internal has a listener address, a dedicated `*.internal` server certificate/key, and the service CA. vmmd independently requires the public CA bundle before it admits guest traffic to bridge port 443 or stages `/etc/faas/service-proxy-ca.crt` into networked guests. The legacy H1/H2C `:10080` endpoint and all generated binding URLs remain unchanged.

## Why a separate endpoint

ADR-243 added binding-scoped `.internal` discovery and authorization, but the
guest-to-node hop still speaks HTTP. A TLS endpoint makes the short name usable
as `https://billing.internal` without coupling its rollout to existing service
clients. This is server-authenticated TLS, not guest mTLS: gatewayd continues to
derive caller identity from the tenant bridge source address and applies the
same binding, account, target allowlist, preview and routing checks. DNS alone
is not authorization; direct Host requests go through the same checks.

`.internal` is a private-use name, so this endpoint requires a Gregale-owned
private CA, distinct from the daemon-to-daemon PKI. The private key stays on
the gateway host. vmmd only receives and stages the public CA certificates.
The leaf has exactly one DNS SAN, `*.internal`, and ServerAuth usage. Gatewayd
checks the leaf against the configured CA at startup and again at each TLS
handshake, allowing leaf rotation without a restart. CA rotation requires a
bundle-overlap rollout and daemon/guest restart or restore.

## Wire and trust contract

The TLS listener accepts H1 and H2 by ALPN so gRPC can use the latter; it
retains the existing service proxy's streaming and upgrade behavior. It rejects
non-alias Host values and Host/SNI mismatches before routing. It binds only to
the same private bridge IP as the existing HTTP listener. The per-VM firewall
admits port 443 only when vmmd has valid public service CA material; the
existing 10080 and DNS rules remain independent.

The CA is **not** added to the guest's global OS trust store. A workload opts
in with a client-specific CA bundle, for example:

```sh
curl --cacert /etc/faas/service-proxy-ca.crt https://billing.internal/healthz
```

This initial slice avoids silently trusting a platform CA outside the service
namespace. Language clients must point their TLS verifier at the staged bundle;
sidecars are not promised that file by this slice. [ADR-245](245-workload-scoped-service-ca-trust.md)
extends this contract with workload-local bundles and common runtime trust
variables, and requires the service CA to be constrained to `.internal` DNS
names.

## Rollout

1. Provision a dedicated private CA and per-node `*.internal` ServerAuth
   leaf/key. Distribute the same public CA bundle to gatewayd-internal and
   vmmd. Do not reuse `/etc/faas/tls/ca/ca.crt` (daemon mTLS).
2. Configure `daemons.gatewayd_internal.service_proxy_https_listen` as
   `<host-bridge-ip>:443` and `service_proxy_tls` with cert/key/CA paths.
   Keep the existing `service_proxy_listen` on the same bridge IP at `:10080`.
   Start and check gatewayd's private TLS listener first.
3. Configure `daemons.vmmd.service_proxy_ca_path` with the public CA path and
   restart vmmd. New/restored app guests receive the bundle and the :443
   firewall rule. Existing live guests need recycling before opt-in. A changed
   bundle invalidates the restore pre-boot digest and is restaged on restore.
4. Canary a declared binding with certificate verification enabled, including
   HTTP/2 or gRPC if applicable. A later PR may change generated binding URLs
   only after every compute node supports the endpoint **and** the calling
   clients have a supported trust configuration. The staged file alone does
   not make arbitrary language runtimes trust the CA. No automatic downgrade
   from HTTPS to HTTP is allowed on the new endpoint.

Removing the vmmd CA setting closes the :443 guest admission rule for new
network namespaces. Removing the gatewayd settings disables the HTTPS
listener. Neither action changes legacy generated URLs or the :10080 path.

## Out of scope

This PR does not enable HTTPS by default, change `GREGALE_SERVICE_*_URL`,
install CA certificates into language-specific or global trust stores, add
service RPC, or replace the existing source-IP-based caller authentication.
