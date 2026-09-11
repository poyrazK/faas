# Static egress IP

Reserve a static egress address when an upstream service requires an IP allowlist.

```bash
gregale app APP_ID static-egress-ip set 203.0.113.42
gregale app APP_ID static-egress-ip show
gregale app APP_ID static-egress-ip clear
```

The address applies to outbound connections from the selected app and region; it does not change inbound routing. Add the address to the upstream allowlist before switching traffic, then verify with a safe endpoint. Removing it can break allowlisted integrations, so treat the operation as a planned change.

Static egress is plan- and region-dependent. The API reports availability and any charge before allocation; do not hard-code an address in application configuration.
