# Deploy an app

Preview first: the dry run says what Gregale will do with this source and which features apply.

- Check the source locally (listener, env, Dockerfile):
    gregale doctor
- Preview the deploy; read its "For this repo" section:
    gregale deploy --dry-run
- Deploy:
    gregale deploy --yes
- Deploy a site people open in a browser, with no API key on its URL:
    gregale deploy --yes --no-require-authn
- Deploy a published image instead of source:
    gregale deploy --image <image>@sha256:<digest> --name <app>
- Verify a response from the accepted deployment (including private apps):
    gregale release verify <deployment> --app <app> --path /
- Open an app that is already deployed to anonymous visitors:
    gregale app update <app> --no-require-authn
- Check serving health:
    gregale app health <app>

Notes:
- A Compose file or several services: see `gregale guide compose-project`.
- Public or private: on Hobby and above a new app's URL answers 401 without an API key, even after a successful deploy. When the user wants a website, a demo or a link to share, or the app was public where it ran before (Render, Heroku), add `access: public` to its gregale.yaml (every deploy keeps it public, MCP deploys too) or deploy with --no-require-authn; keep the default for an API or anything the user did not ask to publish, and tell the user which you chose. release verify checks the deployment with owner access; it does not test anonymous access.
- Readiness: a health route found in the source (or hosting.health) must answer 2xx; with none, an open port is enough. Do not pass / as the health path. An app that checks the Host header (Django ALLOWED_HOSTS) needs the health route answered first; see `gregale guide health-checks`.
