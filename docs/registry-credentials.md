# Registry credentials

Registry credentials let Gregale pull private OCI images during a deployment.

```bash
gregale registry list --app APP_ID
gregale registry set --app APP_ID --registry registry-1.docker.io --user USER --password "$REGISTRY_TOKEN"
gregale registry rm --app APP_ID --registry registry-1.docker.io
```

The CLI prompts for the secret and sends it over the authenticated API; it never prints the value. The service stores write-only credential metadata, redacts credential fields from events and logs, and uses the credential only for the selected registry host.

Prefer short-lived, read-only registry tokens. Pin production images by digest and use `gregale deploy --dry-run` to confirm the image can be resolved before changing an app.
