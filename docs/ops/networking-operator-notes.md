# Networking: operator notes

Installation settings behind the customer [networking page](../networking.md).
They moved here so that page can be published (#5709). Private network fabric
and transport settings are on [private networks](../private-networks.md).

- **Outbound probes.** Configure `outbound_probe_gateway_url =
  "https://outbound.example.com"` in apid.toml and route that trusted,
  guest-reachable origin to outboundd. Without it, binding probes keep
  unsupported coverage.
- **Private HTTPS service listener.** Enable it only after distributing a
  dedicated service CA to all compute nodes. The CA must have a critical
  permitted DNS name constraint for `.internal`.
- **Signed caller assertions.** Set `FAAS_SERVICE_CALLER_ASSERTIONS=1` on every
  node that may originate calls before customers make verified identity
  mandatory; while rollout is mixed, an absent assertion is still possible.
- **Preview service traffic metrics.** Compare isolated traffic in
  `gateway_service_preview_to_preview_total` with allowed production fallbacks
  in `gateway_service_preview_to_production_total`; policy rejections use
  `gateway_service_call_total{outcome="preview_denied"}`.
