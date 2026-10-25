# Registry credentials

Registry credentials let Gregale pull private OCI images during a deployment.

```bash
gregale registry list --app APP_ID
printf '%s\n' "$REGISTRY_TOKEN" | gregale registry set --app APP_ID --registry registry-1.docker.io --user USER --password-stdin
gregale registry rm --app APP_ID --registry registry-1.docker.io
```

The CLI prompts for the secret and sends it over the authenticated API; it never prints the value. The service stores write-only credential metadata, redacts credential fields from events and logs, and uses the credential only for the selected registry host. The credential is sent only to an HTTPS token endpoint; a registry whose authentication challenge names a plain-HTTP token endpoint fails the pull instead.

Image runs (`gregale run`) take the credential per run instead:
`--registry-username USER` with the password from `--registry-password-stdin`,
`GREGALE_REGISTRY_PASSWORD` or a prompt, or from a Docker config with
`--docker-config ~/.docker` (ADR-20261025110000). It is stored sealed on the image's
runner job, for the image's registry only, and refreshed by every run that
passes it (ADR-1790). See [Jobs](jobs.md#image-runs).

Docker Hub limits pulls without a credential per IP address. Operators can set
`faas_docker_hub_mirrors` (for example `mirror.gcr.io`) so that deploys and
Dockerfile builds read public Docker Hub images through those mirrors first
([ADR-20261025114554](adr/20261025114554-docker-hub-mirrors.md)). A pull that
uses a stored credential always goes to Docker Hub itself; the credential is
never sent to a mirror.

Prefer short-lived, read-only registry tokens. Pin production images by digest and use `gregale deploy --dry-run` to confirm the image can be resolved before changing an app.
