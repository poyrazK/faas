# ADR-948: Built validator artifacts and project publication

Status: local implementation, unqualified; shared artifact storage remains default off.

## Decision

For allowlisted apps with shared validator artifacts enabled, builderd reads an
explicit `gregale.validator.json` from the verified source tar.gz at its selected
SourceRoot. The descriptor contains only runtime, entrypoint and explicit base64
source files. It has no app/deployment identities or caller-provided digest. The
trusted lifecycle supplies those identities, computes the shared digest, validates
bundle paths/bytes/runtime, and conditionally publishes before cache lookup or builder
VM dispatch. Cache hits still require validator publication. No arbitrary archive
files, production rootfs, environment variables or host credentials are discovered
or copied into a validator. Validator code is not executed during packaging.

The offline packaging command gains `--source-bundle` to produce this descriptor
without a deployment identity. Commit or include the reviewed descriptor at the
selected source root before ordinary source deployment. Static image deployments
continue to use ADR-947's explicit publisher; source builds require the descriptor.
There is no automatic dependency installation, interpreter compilation or validator
inference from application code.

Archive scans accept one regular descriptor, reject duplicates/symlinks, bound its
encoded bytes by the existing 4 MiB registry limit, count entries with the existing
source archive limit, and cap scanned uncompressed archive bytes at 256 MiB. This
new cap lives in `pkg/api/limits.go`; larger expanded sources cannot use this path.
The ordinary source boundary and checksum checks run first. Release artifacts may
remain referenced by failed builds; there is no implicit cleanup or rebinding.

Environment promotion checks the source binding, then carries identical validator
bytes to the copied deployment identity before dark/live publication. Clone readiness
copies the source binding before notifying prime or reporting a ready workload. The
shared backend is required for these gated copies; process-local registries cannot
safely acquire a new binding. Resumed promotion checkpoints and project release graph
activation/publication recheck member bindings. Previous graph and workload rollback
paths check the restored bindings. Checks remain per-path preflights, not atomic
transactions between SQL and object storage. A failed transfer can leave a pending
candidate or unreferenced bundle; it cannot establish validator readiness.

Deployment detail (`deploymentResponseWithBuild`) gains observational
`durable_entity_validator` metadata: status disabled/unavailable/ready, source
registry/object_storage and verified digest when ready. A two-second diagnostic
budget bounds provider reads. No code, credentials or candidate entity state is
returned. The sample is not a reservation, purity attestation or a fleet acknowledgement.
Other deployment summaries omit this field. Go/Node/Python models reflect the optional
metadata.

Builderd now participates in shared artifact configuration and gets an optional
per-daemon secret EnvironmentFile for S3 credentials. This does not stage that file,
enable any gate or install a unit on a host. Native GCS continues to use ADC; no host
storage credential is added to customer build/validator guest protocols.

## Qualification

Source cases cover source archive publication, SourceRoot selection, missing/unsafe
source roots, artifact transfer, metadata inspection and preservation of the shared
digest. SDK models are generated. Tests, builds, lint/daemon unit checks, live bucket
operations and native build/release acceptance were not run. Qualify cache hits,
cancellation, oversized/duplicate/symlink descriptors, missing-source refusal,
resumed promotion/clone, dark/graph publication, rollback and credential isolation
before enabling the gates. Other direct SQL publication writers still require a
complete release-path inventory; this change does not introduce a universal SQL fence.
