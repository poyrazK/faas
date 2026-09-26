# ADR-273: Workload-scoped trust for private service bindings

- **Status:** accepted
- **Date:** 2026-09-25
- **Decision:** When vmmd stages the service CA, guest-init creates a workload-local CA bundle under that workload's writable `/tmp`, makes the path available as `GREGALE_SERVICE_CA_BUNDLE`, and configures common TLS clients to use it without editing the image or global OS trust store. The configured CA must carry a critical permitted DNS name constraint for `.internal`; gatewayd-internal and vmmd both reject unconstrained service CAs.

## Why

ADR-272 made the HTTPS endpoint opt-in and left certificate selection to each
client. A staged certificate path was insufficient for sidecars and required
applications to know a platform-internal file location. The guest can expose a
stable workload-local path while preserving image roots and the read-only
sidecar filesystem.

Automatically placing a private CA in generic TLS trust variables broadens the
names that client libraries may accept. Requiring a critical `.internal` DNS
name constraint makes the platform's trust injection match the namespace the
service proxy is allowed to serve. This constraint is checked both where the
gateway configures the TLS listener and where vmmd admits/stages the CA.

## Workload contract

When service CA material is present, guest-init combines it with the workload
image's first available standard PEM CA bundle and writes the result to
`/tmp/.gregale-service-proxy-ca-bundle.pem`. It also writes the service CA alone
to `/tmp/.gregale-service-proxy-ca.pem`. Both paths live on the workload's
bounded tmpfs and are recreated at process start; images and snapshots are not
modified.

The guest exports `GREGALE_SERVICE_CA_BUNDLE` as the combined bundle path. If a
standard system CA bundle exists in the image, it also sets `SSL_CERT_FILE`,
`REQUESTS_CA_BUNDLE`, and `CURL_CA_BUNDLE` to that combined bundle. Node receives
the service CA alone through `NODE_EXTRA_CA_CERTS`, which adds it to Node's
existing default roots. If no standard system bundle exists, the guest leaves
the common trust variables unchanged rather than replacing a runtime's roots
with a private-only bundle; the canonical path and Node's additive CA remain
available. Platform-managed variables replace customer values only when the
service CA is configured (and a system bundle exists for the common bundle
variables). Clients which ignore these environment variables (for example,
custom TLS stacks or Java trust stores) can explicitly use
`GREGALE_SERVICE_CA_BUNDLE` with their own verifier. Certificate verification
is never disabled.

Main workloads, legacy sidecars, and full-rootfs sidecars receive the same
contract. Full-rootfs sidecar copies are created within that sidecar's private
`/tmp`; the guest never writes into its read-only image root. With no staged
service CA, guest-init does not create a bundle or modify TLS environment.

## Rollout and compatibility

The HTTPS listener, bridge firewall rule, and URL generation remain opt-in and
unchanged. Operators using ADR-272's pre-existing service CA must replace it
with one whose certificate has a **critical permitted DNS name constraint of
`.internal`** before enabling guest trust. Existing manual use of
`/etc/faas/service-proxy-ca.crt` remains available to applications that prefer
per-request CA selection. CA rotation retains ADR-272's overlap-and-restart
requirements.

This does not switch generated binding URLs to HTTPS, install the CA into the
guest's global store, or claim automatic support for every language-specific
trust implementation. URL cutover remains a separate rollout step.

## Rejected alternatives

- **Install into each image's OS trust store:** images vary, sidecar roots are
  read-only, and this would be a broader mutation than needed.
- **Expose only the staged `/etc/faas` path:** full-rootfs sidecars do not share
  that path, and applications would have to hard-code Gregale's storage layout.
- **Inject an unconstrained private root into standard runtime settings:** this
  would let that CA authenticate names outside the private service namespace.
