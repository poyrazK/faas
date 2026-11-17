# Registry credentials

Registry credentials let Gregale pull private OCI images during a deployment.

```bash
gregale registry list --app APP_ID
printf '%s\n' "$REGISTRY_TOKEN" | gregale registry set --app APP_ID --registry registry-1.docker.io --user USER --password-stdin
gregale registry delete --app APP_ID --registry registry-1.docker.io
```

The CLI prompts for the secret and sends it over the authenticated API; it never prints the value. The service stores write-only credential metadata, redacts credential fields from events and logs, and uses the credential only for the selected registry host. The credential is sent only to an HTTPS token endpoint; a registry whose authentication challenge names a plain-HTTP token endpoint fails the pull instead.

Image runs (`gregale run`) take the credential per run instead:
`--registry-username USER` with the password from `--registry-password-stdin`,
`GREGALE_REGISTRY_PASSWORD` or a prompt, or from a Docker config with
`--docker-config ~/.docker`. It is stored sealed on the image's
runner job, for the image's registry only, and refreshed by every run that
passes it. See [image runs](image-runs.md).

A Dockerfile build uses the same credentials for its `FROM` images, so an app can
build on a private base image.
The build log names the registries it authenticates to. Such a build never reuses
a warm builder, and its cached image is reused only by the same app. An ECR access
key cannot be used for `FROM` images; store an ECR password with the username `AWS`
for those, or build on a public base.

## Cloud registries


| Registry | `--registry` | `--user` | Password |
|---|---|---|---|
| Amazon ECR | `<account>.dkr.ecr.<region>.amazonaws.com` | An IAM access key ID (`AKIA…`) | The secret access key |
| Google Artifact Registry | `<region>-docker.pkg.dev` | `_json_key_base64` | The base64-encoded service account key |
| Azure Container Registry | `<name>.azurecr.io` | A service principal's application ID, or a repository token's name | Its secret or token password |

- **ECR.** Gregale exchanges the access key for ECR's 12-hour registry password at pull time and never sends the key to the registry. Use an IAM user that has only the `AmazonEC2ContainerRegistryReadOnly` policy. Temporary keys (`ASIA…`) are not accepted. A stored `AWS` username with an ECR password also works, until the password expires 12 hours later.
- **Artifact Registry.** Grant the service account `roles/artifactregistry.reader`.
- **ACR.** Grant the `AcrPull` role, or use a token scoped to the repository.

Docker Hub limits pulls without a credential per IP address. Where Gregale has
a Docker Hub mirror configured (such as `mirror.gcr.io`), deploys and Dockerfile
builds read public Docker Hub images through it first. A pull that
uses a stored credential always goes to Docker Hub itself; the credential is
never sent to a mirror.

Prefer short-lived, read-only registry tokens. Pin production images by digest and use `gregale deploy --dry-run` to confirm the image can be resolved before changing an app.
