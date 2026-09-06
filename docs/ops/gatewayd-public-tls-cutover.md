# Current-edge TLS cutover drill

This is the current M8 operator procedure for issue #252. `gatewayd-public`
does not terminate TLS; Caddy and Cloudflare own the public HTTPS edge and
forward to the daemon's loopback HTTP listener. The legacy
`gatewayd-tls-cutover.md` and `2026-07-21-tls-cutover.md` documents describe
the retired monolithic daemon and are kept only for historical reference.

Run the safe local rehearsal with:

```sh
make tls-cutover-drill
```

The target defaults to `TLS_CUTOVER_MODE=dry-run`. It writes a dated evidence
record without reloading Caddy, changing DNS, or sending customer traffic. On
the reference control-plane host, after reviewing the command hooks, run:

```sh
sudo TLS_CUTOVER_MODE=execute make tls-cutover-drill
```

The Ansible `site.yml` play installs the same helper at
`/usr/local/sbin/faas-tls-cutover-drill` and creates `/var/lib/faas` for the
dashboard state file. All command hooks are overridable so a provider-specific
DNS or Caddy wrapper can be used without editing the drill:

| Variable | Default | Purpose |
|---|---|---|
| `FAAS_TLS_VALIDATE_CMD` | `caddy validate --config /etc/caddy/Caddyfile` | Validate the edge config |
| `FAAS_TLS_DNS_CHECK_CMD` | `command -v cloudflare-api` | Verify the DNS control tool |
| `FAAS_TLS_CUTOVER_CMD` | `systemctl reload caddy` | Apply the edge config |
| `FAAS_TLS_SMOKE_CMD` | `curl --fail --silent --show-error --head https://api.gregale.dev` | Verify HTTPS after cutover |
| `FAAS_TLS_ROLLBACK_CMD` | `systemctl reload caddy` | Restore the prior edge config |
| `FAAS_TLS_POST_ROLLBACK_CMD` | same as smoke | Verify the rollback |
| `FAAS_TLS_DNS_PROVIDER` | `cloudflare` | Evidence label |
| `FAAS_TLS_DNS_TOOL` | `cloudflare-api` | Evidence label; set to `rclone` or `transip-cli` for those operator paths |
| `FAAS_TLS_CUTOVER_STATE_FILE` | `/var/lib/faas/tls-cutover.state` | Durable dashboard state |

The five steps are deliberately explicit:

1. Confirm the endpoint, provider, operator, and host inputs.
2. Validate Caddy and the configured DNS control path.
3. Reload the edge and verify the public certificate/HTTPS response.
4. Exercise rollback and persist the rollback state.
5. Verify the endpoint again and confirm `/dashboard/admin` still shows the
   drill banner.

The state file contains only the run ID, timestamps, operator, state, and a
short message. It contains no DNS tokens, cookies, or private key material.
The admin dashboard reads it after the drill, so the banner remains visible
after rollback instead of disappearing with the active edge configuration.
