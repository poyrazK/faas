# Sidecars

Sidecars are bounded helper containers that run beside an app instance. Use them for telemetry, protocol adapters, or local caching—not for a second application control plane.

Declare each sidecar in the deployment request with a digest-pinned image, an explicit command, and a resource budget. Keep the interface narrow (usually localhost or a named Unix socket), make startup and shutdown graceful, and send logs to stdout/stderr. The API accepts at most two entries: one `init` and one long-running `sidecar`.

```bash
gregale deploy --dry-run
```

The dry run reports image, CPU/RAM budget, ports, and plan compatibility before upload. Sidecars share the app's lifecycle and failure domain, so a crash or resource spike can affect the main process. Keep them stateless and safe to restart.
