# ADR-20261025124445 · Cloud registries: Basic challenges and ECR access keys

- **Status:** accepted
- **Date:** 2026-10-25
- **Amends:** ADR-062 (private-registry credentials), ADR-1790 (image-run credentials)
- **Decision:**
  - **Basic challenges.** When a registry answers `401` with a `Basic` challenge and no `Bearer` realm, a pull that carries a credential retries the request with HTTP Basic authentication, over HTTPS only. Amazon ECR and registries behind plain htpasswd answer this way. Before this, the client asked them for a Bearer token they do not issue, so every pull from them failed.
  - **ECR access keys.** A credential stored for an ECR registry (`<account>.dkr.ecr[-fips].<region>.amazonaws.com[.cn]`) whose username is an IAM access key ID (`AKIA…`) is treated as that key; the password is the secret access key. imaged signs `GetAuthorizationToken` with it (SigV4, at `api.ecr.<region>.amazonaws.com`) and pulls with the 12-hour password ECR returns. Exchanged passwords are reused in memory until 30 minutes before they expire.
    - The secret key is never sent to the registry. Errors are scrubbed of both the key and the exchanged password.
    - A credential whose username is `AWS` keeps working as before: it is an ECR password the customer refreshes.
    - Temporary keys (`ASIA…`) are not accepted: they need a session token the credential has no field for.
  - **Same path everywhere.** The exchange happens inside the OCI client, so app registry credentials and image-run credentials (`--registry-username`, `--docker-config`) both get it. An ECR credential helper in a Docker config (`ecr-login`) already yields an `AWS` password, which now works through the Basic challenge.
- **Why:** ECR passwords expire after 12 hours, so a stored ECR credential stopped working the next day, and the Basic challenge meant even a fresh one could not pull.
- **Google Artifact Registry and Azure Container Registry** need no change. Their token endpoints take Basic credentials:
  - GAR: username `_json_key_base64`, password the base64 service account key (role `roles/artifactregistry.reader`).
  - ACR: a service principal (application ID and secret), or a repository-scoped token's name and password.
  `docs/registry-credentials.md` now documents all three.

## Consequences

- Recommend a dedicated IAM user with only `ecr:GetAuthorizationToken`, `ecr:BatchGetImage` and `ecr:GetDownloadUrlForLayer` (the `AmazonEC2ContainerRegistryReadOnly` policy).
- imaged reaches `api.ecr.<region>.amazonaws.com` through the egress-guarded client.
- Builder VMs still pull `FROM` images anonymously; private base images remain a separate decision.
