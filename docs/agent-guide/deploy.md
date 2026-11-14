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
- Serve the public URL without a Gregale API key:
    gregale app update <app> --no-require-authn
- Check serving health:
    gregale app <app> health

Notes:
- A Compose file or several services: see `gregale guide compose-project`.
- Public URLs answer 401 without a Gregale API key until --no-require-authn.
- A source build must answer HTTP GET /healthz on its port; see `gregale guide health-checks`.
