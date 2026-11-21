# Deploy an app

Preview first: the dry run says what Gregale will do with this source and which features apply.

- Check the source locally (listener, env, Dockerfile):
    gregale doctor
- Preview the deploy; read its "For this repo" section:
    gregale deploy --dry-run
- Deploy:
    gregale deploy --yes
- Deploy a published image instead of source:
    gregale deploy --image <image>@sha256:<digest> --name <app>
- Verify a response from the accepted deployment (including private apps):
    gregale release verify <deployment> --app <app> --path /
- Optionally allow anonymous public requests:
    gregale app update <app> --no-require-authn
- Check serving health:
    gregale app health <app>

Notes:
- A Compose file or several services: see `gregale guide compose-project`.
- A private URL can return 401 without credentials after a successful deploy. release verify checks the exact deployment using owner access; it does not verify anonymous public access.
- Readiness: a health route found in the source (or hosting.health) must answer 2xx; with none, an open port is enough. Do not pass / as the health path. An app that checks the Host header (Django ALLOWED_HOSTS) needs the health route answered first; see `gregale guide health-checks`.
