# Health checks and readiness

An instance takes traffic once it passes readiness, and is restarted when it keeps failing liveness.

- Check an app's serving health and readiness:
    gregale app <app> health
- Use another health path (or set hosting.health in gregale.yaml):
    gregale app <app> --health-path <path>
- Deploy a raw TCP server from its published image (TCP readiness):
    gregale deploy --image <image>@sha256:<digest> --name <app>
- Read why a deploy did not become ready:
    gregale logs <app> --deployment <deployment>

Notes:
- A Compose healthcheck: runs inside the instance and replaces the default probe.
- A source build without one is probed with HTTP GET /healthz on its serving port; a 2xx is ready.
- A Dockerfile HEALTHCHECK adds a command check; it does not replace the HTTP probe.
- A directly deployed image is ready once its port accepts connections.
- Redis, PostgreSQL and brokers never answer HTTP: built from source they fail with app_startup_timeout.
