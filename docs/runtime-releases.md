# Runtime identity and upgrade previews

Gregale-managed function artifacts can record an immutable runtime base release.
The identity covers the OCI source digest, architecture, guest startup binary,
base layout and exact published base bytes. The host kernel and function runner
are separate components. A source language declaration or builder image digest
does not establish the deployed runtime identity.

Inspect one deployment:

```sh
gregale deployment runtime v42 --app my-function
# Machine-readable evidence:
gregale deployment runtime v42 --app my-function --json
```

The console shows the same evidence under **Release details → Runtime**. An
older artifact without a recorded binding shows **unknown**. It retains its
legacy logical-base behavior; a fresh deployment records a binding. Ordinary
apps, customer images/Dockerfiles, standalone Jobs and sidecars are outside this first
slice. No inferred runtime identity is offered for them.

Choose an exact published candidate from the returned catalogue to preview:

```sh
gregale deployment runtime v42 --app my-function --target RELEASE_ID --json
```

The catalogue contains at most 50 recent publications for the current family
and architecture. Candidates appear after a managed function build publishes
them. A publication is not a tested upgrade and its position in the catalogue
does not prove a newer language patch. The preview compares component identities
and describes blockers, required rebuilding, fresh cold boot and rollout steps.
Changing families or architectures requires an explicit application migration.

The preview changes no deployment, traffic, source or environment. Applying and
scheduling updates are not implemented yet. A future updater must qualify the
target, rebuild the same source, obtain fresh candidate readiness, and use the
existing guarded rollout and rollback. Health history is useful for
investigation but cannot itself authorize an update. Older snapshots cannot be
reused across different backing bytes; existing snapshot checks enforce this.

REST reads: `GET /v1/deployments/{id}/runtime` and
`GET /v1/deployments/{id}/runtime/upgrade-preview?target=RELEASE_ID`.
Both require read authorization and deployment ownership. See
[ADR-596](adr/596-immutable-runtime-releases.md) for publication, retention and
native acceptance requirements.

Runtime metadata bounds are 1,024 bytes for the physical artifact key, 64 bytes
for the layout identifier and 4,096 bytes for reading the source/guest-startup
sidecar. Local split-box installations must update their artifact handoff helper
to support `--runtime-release BASE_KEY`; the bundled helper transfers the base
and its content/scan evidence before the deployment can use it. Multi-node
publication requires a shared artifact backend.
