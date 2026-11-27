# Health checks and readiness

An instance takes traffic once it passes readiness, and is restarted when it keeps failing liveness.

- Check an app's serving health and readiness:
    gregale app health <app>
- Use another health path (or set hosting.health in gregale.yaml):
    gregale app <app> --health-path <path>
- Deploy a stateless TCP server (a broker, memcached) from its image:
    gregale deploy --image <image>@sha256:<digest> --name <app>
- Read why a deploy did not become ready:
    gregale logs <app> --deployment <deployment>

Notes:
- A Compose healthcheck: runs inside the instance and replaces the default probe.
- A source build is probed with HTTP GET on the health route found in its source (/healthz, /health, /readyz, /ready, /livez, /live) or named in hosting.health; a 2xx is ready. With neither, it is ready once its port accepts connections.
- Do not use / as the health path: the edge can answer a public health path itself, so visitors to / would get {"status":"ok"} instead of the page.
- The probe sends the instance's internal address as Host. An app that rejects unknown hosts (Django ALLOWED_HOSTS) fails with app_startup_timeout unless it answers the health path before that check, in a middleware: https://gregale.dev/docs/deploy-django#health-check. Do not add the internal address to the allow-list.
- A Dockerfile HEALTHCHECK adds a command check; it does not replace the HTTP probe.
- A directly deployed image is ready once its port accepts connections.
- A message broker or memcached never answers HTTP: built from source it fails with app_startup_timeout; deploy its image.
- Datastores (Redis, PostgreSQL, MySQL, MongoDB) are not run at all; see `gregale guide compose-project`.
