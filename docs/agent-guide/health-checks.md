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
- A source build without one is probed with HTTP GET /healthz on its serving port; a 2xx is ready.
- A Dockerfile HEALTHCHECK adds a command check; it does not replace the HTTP probe.
- A directly deployed image is ready once its port accepts connections.
- A message broker or memcached never answers HTTP: built from source it fails with app_startup_timeout; deploy its image.
- Datastores (Redis, PostgreSQL, MySQL, MongoDB) are not run at all; see `gregale guide compose-project`.
