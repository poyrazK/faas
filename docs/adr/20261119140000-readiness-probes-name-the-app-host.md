# ADR-20261119140000: Readiness probes name the app's public host

- Status: Proposed (switch `readiness_probe_app_host` off until prod-tested;
  the owner decides when to turn it on)
- Date: 2026-11-19
- Amends: ADR-053/057 (per-deployment readiness path), ADR-079 (liveness),
  ADR-20261114153000 (primed init snapshots)

## Context

vmmd's readiness GET (cold boot and restore) and the init-snapshot prime
burst dial the guest at `<HostIP>:8080` and send that address as `Host`. The
liveness and readiness loops ask guest-init to probe `127.0.0.1:<port>`,
which sends the loopback address. Public traffic arrives with the app's own
host (`<slug>.gregale.dev`) and `X-Forwarded-Proto: https`.

An app that allow-lists its host names answers the probe differently from its
traffic. Django with `ALLOWED_HOSTS` set to its public host, exactly as
`docs/deploy-django.md` tells customers to set it, answers every probe
`400 DisallowedHost`; readiness never passes and the first deploy fails with
`app_startup_timeout`. Rails `config.hosts` and other host allow-lists behave
the same way. In the 2026-11-19 agent eval (rc.357, `migrate-django-heroku`)
the readiness probe got 400 187 times in 30 s, and coding agents in two
separate runs each worked around it by writing a middleware that answers
`/healthz` before Django's host check. A customer following the docs has no
such workaround.

The owner response check (`release verify`, `cmd/gatewayd-internal`) already
sends the app's public host and, since #7623, `X-Forwarded-Proto: https`.

## Decision

1. **Host.** For a production-scope deployment schedd computes the app's
   public host, `<slug><apps suffix>` (`hostidentity.BuildPrimaryAppHost`
   over schedd's `apps_domain`), and sends it to vmmd as the new
   `AppSpec.probe_host` (field 46). It is empty, keeping today's probe, for
   environment and preview scopes (served on other hosts), when the apps
   domain is empty (custom-domain-only operators), and for a slug that does
   not form a valid host.
2. **Which probes.** vmmd's startup readiness GET on cold boot and on
   restore, the prime burst, and the liveness and readiness loops: vmmd passes
   the host to guest-init in the liveness request's new optional `host` JSON
   field. Each of those GETs then sends `Host: <probe_host>` and
   `X-Forwarded-Proto: https`. The dial target does not change. gRPC probes,
   image health checks and sidecar probes are unchanged.
3. **Adoption.** The host is saved in the adoption record (`probe_host`), so
   an adopted instance's loops keep naming it after a vmmd restart.
4. **Compatibility.** Every piece is additive. An older vmmd ignores
   `probe_host`; an older guest-init ignores `host`; an older adoption record
   carries none. Each falls back to today's probe.
5. **Switch.** schedd `readiness_probe_app_host` (env
   `FAAS_READINESS_PROBE_APP_HOST`), off by default. With it off schedd sends
   no `probe_host` and nothing changes.

## Consequences

- A Django app whose health path is a plain view, with `ALLOWED_HOSTS` set
  to its public host, passes readiness once the switch is on and the release
  with this guest-init is live. Until then `docs/deploy-django.md` answers
  `/healthz` in a middleware placed before `SecurityMiddleware`, which runs
  before Django's host check and its HTTPS redirect. The middleware stays
  correct after the switch; the docs keep it while environment and preview
  scopes still probe with the instance address (below).
- An app that answered the instance address but rejects its own public host
  would now fail readiness. Such an app also rejects its traffic, so it was
  not serving customers.
- Probes are visible in app logs and metrics as requests to the app's public
  host. They still carry no client IP and no `X-Forwarded-For`.
- Not covered: environment and preview scopes still probe with the instance
  address. A later step can send their environment host
  (`hostidentity.BuildEnvironmentHost`) once the wake carries the environment
  identity.

## Rollout

1. Merge with the switch off; release (guest-init ships in the base image).
2. Run metal (`make test-metal`) on the release: wake, restore and liveness
   paths.
3. Turn the switch on for one node's schedd, deploy a Django app configured
   as `docs/deploy-django.md` says but with `/healthz` as a plain `urls.py`
   view named by `hosting.health` (no middleware), with
   `SECURE_SSL_REDIRECT` on, and confirm readiness passes and
   liveness stays green across a park and wake and a vmmd restart.
4. Turn it on fleet-wide. Drop the health middleware from the Django docs
   only once environment and preview scopes send their host too.
