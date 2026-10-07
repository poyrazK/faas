# Deploy an image after publishing it

Compose image workloads can deploy a newly published artifact without changing
their image tag in Git. First apply the project with a stateless image service:

```yaml
services:
  api:
    image: ghcr.io/team/api:production
    ports: ["8080:3000"]
```

After CI builds and pushes the image, pass the build's immutable digest:

```sh
gregale registry published --app api \
  --image "ghcr.io/team/api@sha256:<64-lowercase-hex-digest>" --wait
```

Use `FAAS_API` and `FAAS_TOKEN` in CI. An app-bound deploy token limits the
credential to this workload; existing CI/OIDC bearers and `deploy:write` API
keys also work. Store the credential in the CI secret store. Private image
pulls use the workload's existing Gregale registry credentials.

The registry and repository must match the workload's image declaration. Tags
are rejected. A declaration pinned with `@sha256:...` accepts only that digest;
change the declaration to authorize a new one. The tag in Compose stays intact
when this trigger accepts a new digest.

An example handoff after a GitHub Actions build/push step named `push`:

```yaml
- name: Deploy published image
  env:
    FAAS_API: ${{ vars.FAAS_API }}
    FAAS_TOKEN: ${{ secrets.FAAS_DEPLOY_TOKEN }}
    PUBLISHED_IMAGE: ghcr.io/team/api@${{ steps.push.outputs.digest }}
  run: gregale registry published --app api --image "$PUBLISHED_IMAGE" --wait
```

This step assumes the Gregale CLI is installed. Use the digest returned by the
push step, rather than resolving a mutable tag again. Configure CI concurrency
or sequencing when multiple pipelines publish the same workload.

`--wait` returns failure if the deployment fails or the wait times out. Add
`--json` for the deployment receipt. Without `--wait`, the command reports
acceptance and the deployment continues asynchronously. Use `--scope staging`
for an existing deployment scope or `--environment staging` for a registered
project environment; the flags are mutually exclusive.

The equivalent API call is:

```http
POST /v1/apps/api/image-published
Authorization: Bearer <app-bound-deploy-token>
Content-Type: application/json

{"image":"ghcr.io/team/api@sha256:<64-lowercase-hex-digest>","scope":"staging"}
```

The request accepts the normal JSON deployment options, including explicit
overrides and rollout policy. Normal signature, security, plan, and deploy-rate
checks apply. Gregale preserves the Compose command and container port, service
bindings, and scoped environment settings. The source release command and
omitted workflows come from the latest image deployment in that same scope.
The accepted Compose command, including the choice to inherit OCI CMD, is
captured with each new image deployment. Later app edits or a transition to a
source build do not change that command while the image is queued or retried.
Compose healthcheck overrides are captured with each accepted image too.
Partial timing settings inherit omitted values from that digest's image
healthcheck; disabling or replacing the check remains stable after app edits,
retries, and rollback. Existing guest execution applies, while TCP/HTTP readiness
and public-route verification continue to govern traffic promotion. See
[ADR-682](adr/682-compose-image-healthchecks.md).
Explicit deployment command overrides keep their existing precedence.
Newly assembled image releases require a fresh successful effective OCI or
Compose command healthcheck before readiness and promotion. A failed or missing
check leaves the serving release in place. Startup grace, retries and command
timeouts fit within the startup deadline; restores cannot reuse earlier passes.
See [ADR-683](adr/683-image-healthcheck-readiness.md) for compatibility and the
pending native qualification gate.
For a new scope, supply workflows explicitly if required; no prior release
command is inferred. Previous per-deployment overrides are not automatically
copied. Readiness and public-route verification run before traffic cutover.

An image deployment cannot become live after a newer deployment has been
accepted for the same app and scope: the older in-flight candidate becomes
superseded and leaves the current serving release intact. This also applies
when the newer intent fails or is cancelled. Explicit rollback can still select
an older release. The order is Gregale's acceptance order; CI must sequence its
publication calls when overlapping builds finish out of order. See
[ADR-681](adr/681-image-deployment-promotion-ordering.md).

The first delivery returns **202** and a deployment. Repeating the same app,
scope, and digest returns **200** with the original deployment's current
status. Concurrent retries create one durable row. Each scope has its own
identity. Changes to options on a duplicate delivery do not alter the original
release. A replay after failure, cancellation, supersession, or rollback also
returns the original row; it does not undo that outcome. Use the normal retry
or deploy command for an intentional retry or configuration-only redeploy.

This endpoint is the CI handoff. Native registry webhook payloads and automatic
registry polling are outside this first slice. See [ADR-679](adr/679-image-published-deployment-trigger.md).
