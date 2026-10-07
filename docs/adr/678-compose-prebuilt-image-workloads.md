# ADR-678: Compose prebuilt image workloads

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Compose services with `image:` and no `build:` become deployable
  workloads unless the image is on the datastore denylist. Services declaring
  both keep the source build; `image:` is their Compose output tag. Project
  metadata retains the normalized image, command arguments, and main container
  port across reconciliation, GitHub pushes, and pull-request previews.
- **Why:** Direct OCI container deployments already support stateless images
  through platform selection and full-rootfs fallback. Discovery's historical
  shared-base restriction unnecessarily excludes those same images from a
  multi-service project. This refines the discovery restriction in ADR-040;
  existing OCI compatibility and full-rootfs plan gates remain authoritative.
- **Consequences:** Project image deployments create `kind=image` deployment
  rows and notify imaged without queuing builderd. Apply returns a deployment
  ID with no build ID for these workloads. The worker resolves tags using
  existing per-app registry credentials, verifies signatures against the
  immutable source, and pins that source before materialization. Config,
  layers, and full-rootfs fallback read the selected immutable child. Retries
  retain the pinned source. Scan plans show declared image intent; they do not
  contact registries or promise to freeze a tag at preview time.
- **Configuration:** The first published TCP container target selects the main
  port; host bind ports do not configure Gregale ingress. The existing command
  string/shell and argv forms replace OCI CMD while retaining ENTRYPOINT.
  `depends_on`, internal `expose:` ports, service policies, and env-key discovery
  follow the existing project contract. Compose environment values are not
  published in scan plans; customer-managed secrets and env remain authoritative.
  Background image workloads default to worker/job execution modes under the
  existing lifecycle plan gates, rather than requiring an HTTP listener.
- **Boundaries:** Stateful services remain external managed requirements.
  Profiles, mounts, persistent volumes, and Docker lifecycle conditions keep
  their existing semantics. Source-defined operations on image workloads are
  rejected before deployment creation until image admission can atomically
  publish their contracts. Moving a tag does not itself trigger reconciliation;
  a changed project source or image declaration must enqueue a new deployment.

No host Docker build or new VM lifecycle is introduced. Container deployment
retains its existing beta status and Linux/amd64 compatibility boundary.
