# Packaging a durable entity restore validator

This is local, unqualified release tooling. No registry has been installed by this
work. Use [ADR-856](../adr/856-durable-entity-validator-release-tooling.md) for scope
and [qualification](FaasDurableEntityQualification.md) for acceptance requirements.

Create the deployment identity through the existing release process. Supply a
reviewed, secret-free one-shot validator entrypoint for that exact application
schema. Keep app credentials and production configuration outside this directory.
Node validators export a default input function; Python validators define
`main(input, context)`. The input/verdict protocol is ADR-853's existing contract.

The following is an example release hook; identities are non-secret release variables.
Run the Go command only in your build/release environment, not as validator execution:

```bash
go run ./cmd/durable-validator-registry \
  --app "$GREGALE_APP_ID" \
  --deployment "$GREGALE_DEPLOYMENT_ID" \
  --runtime node22 \
  --root examples/durable-entity-sdk \
  --entrypoint isolated-restore-validator.mjs \
  --file isolated-restore-validator.mjs \
  --output /path/to/new-validator-registry.json
```

For subsequent releases, add `--registry /path/to/current-validator-registry.json`.
Repeat `--file` for every reviewed dependency; the hook does not install packages or
infer imports. Use a new output path each time. Existing deployment bindings cannot
change their bytes or app identity. Preserve validators for rollback deployments.
Serialize jobs so parallel generation cannot lose each other's additions.

Stage the complete output using the existing operator deployment process, configure
`FAAS_DURABLE_ENTITY_RESTORE_VALIDATOR_BUNDLES_FILE` to its path and restart all API
writers. A local artifact alone does not register a validator on a running API.
Keep isolation and release gates disabled until qualification is complete. To enforce
local availability on explicit API traffic changes, set
`FAAS_DURABLE_ENTITY_VALIDATOR_RELEASE_GATE_ENABLED=1` alongside ADR-854's isolation
prerequisites. Missing bundles produce `durable_entity_validator_release_required`.
Reducing target traffic can increase a predecessor's traffic, so its bundle is required
as well. Initial build publication and autonomous scheduler/recovery paths are outside
this gate; preload all their candidate and fallback validator bindings independently.

After qualification, standalone restore validation returns the loaded digest. Copy
it into `validation_bundle_sha256` along with the selected `validation_deployment_id`
for restore. A validator digest does not attest to code purity or reserve a state
version. The platform still applies restore fencing and revalidates before commit.

The testing agent should run the new command/shared-registry/APID cases, verify
identical release reproduction and old-binding retention, tampering and symlink
rejection, output collision protection, missing-bundle promotion rejection and traffic
redistribution across predecessors. Qualify actual staging/restarts on all writers;
no fleet-ready claim follows from a single process passing preflight.

## Shared private artifact backend (ADR-857)

The local implementation can replace registry staging/restarts with conditional
private object storage. Keep this mode disabled until qualification. Set the same
non-secret configuration and app allowlist on the release publisher, API writers,
imaged and schedd:

```bash
FAAS_DURABLE_ENTITY_VALIDATOR_ARTIFACTS_ENABLED=1
FAAS_DURABLE_ENTITY_VALIDATOR_PROVIDER=gcs
FAAS_DURABLE_ENTITY_VALIDATOR_BUCKET=dedicated-private-validator-bucket
FAAS_DURABLE_ENTITY_APPS=your-canonical-app-uuid
```

Native GCS uses normal ADC. Optional
`FAAS_DURABLE_ENTITY_VALIDATOR_GCS_IMPERSONATE_SERVICE_ACCOUNT` selects impersonation.
Use each daemon's established credential mechanism; do not copy account credentials
into validator source or guest input. For S3, additionally set
`FAAS_DURABLE_ENTITY_VALIDATOR_ENDPOINT`, `FAAS_DURABLE_ENTITY_VALIDATOR_REGION` and
secret `FAAS_DURABLE_ENTITY_VALIDATOR_ACCESS_KEY`,
`FAAS_DURABLE_ENTITY_VALIDATOR_SECRET_KEY` and optional
`FAAS_DURABLE_ENTITY_VALIDATOR_SESSION_TOKEN` in existing per-daemon secret files.
The API also requires the existing entity, restore validation, isolation and validator
release gates. In shared mode, its startup registry path is unused.

After packaging the reviewed source using the existing command, publish before
allowing candidate priming:

```bash
go run ./cmd/durable-validator-registry \
  --publish-registry /path/to/new-validator-registry.json
```

This is a release-hook command, not something run by this implementation session.
The hook creates app-scoped immutable content and deployment bindings. It refuses a
changed digest for an existing deployment. A failed batch can be partially published;
retry the same reviewed artifact. Publish new code under a new deployment identity.
API writers resolve newly published bindings without restart. App credentials and
production rootfs contents are not automatically extracted into the bundle.

Publisher permission should cover private conditional creation and reads; consumers
need private reads. Existing provider startup paths may have separate credential
requirements. Do not grant public bucket access or lifecycle deletion for
`gregale/durable-entity-validators/v1/`. There is no automatic artifact pruning. Keep
old deployment bindings and content for rollback and investigations.

The initial imaged publication and schedd prime paths now refuse missing artifacts;
APID promotion, canary advance and the covered rollback worker paths read the shared
binding. Existing serving-app wakes remain available during artifact storage outages.
These are per-path preflights, not a universal SQL fence. Project/environment clone
and other separately implemented publication paths need explicit qualification and
coverage before enabling them for these apps. Mixed-version daemons or inconsistent
app allowlists can bypass checks; stage the common code/configuration on every owner.

Extend qualification with two independent readers seeing a just-published binding,
lost content/binding acknowledgements, changed-digest refusal, unknown fields,
missing/corrupt content, no local-registry fallback, failed prime without VM allocation,
failed initial live publication, predecessor rollback and serving wakes during a bucket
outage. Verify actual permissions and bucket lifecycle policy on the dedicated GCS
acceptance bucket. Builds/tests and all live qualification remain pending.

## Automatic source-build publication and copied deployments (ADR-858)

Create a reviewed descriptor before uploading the normal source tarball:

```bash
go run ./cmd/durable-validator-registry \
  --source-bundle \
  --runtime node22 \
  --root path/to/reviewed-validator \
  --entrypoint validate.mjs \
  --file validate.mjs \
  --output path/to/application/gregale.validator.json
```

Include additional files with repeated `--file`; no imports are inferred. Place
`gregale.validator.json` at the application's selected SourceRoot, not an arbitrary
repository directory. It carries explicit runtime, entrypoint and base64 code files.
Review it for secrets before committing/uploading. No app or deployment ID is needed.
Existing output files are protected; create a new artifact and update the source tree
through your normal reviewed release process.

Configure builderd with the same ADR-857 artifact provider and app allowlist as API,
imaged and schedd. Its optional private S3 EnvironmentFile is
`/etc/faas/secrets/builderd/builderd.env`; provisioning and permissions remain operator
steps. GCS uses the established ADC identity. Then ordinary verified source builds
publish validators automatically before cache/VM work. Missing or invalid descriptors
fail the build for these allowlisted apps. The expanded archive scan cap is 256 MiB;
the descriptor retains the 4 MiB encoded limit. Direct image deployments still need
an explicitly published bundle.

Promotion and cloning reuse the validator of the copied source artifact under the
new deployment identity. Shared storage must be enabled for gated copies. Graph
activation and rollback check their member bindings. Inspect the deployment detail's
`durable_entity_validator` status and digest; unavailable can mean missing, invalid or
an unavailable provider. It is a bounded current observation, not a future guarantee.

Native qualification must prove the normal source-build/cache-hit → immutable upload →
prime → live flow, metadata inspection, project/environment copies and rollback.
Include interrupted transfers, resumed checkpoints and provider failures. No such
acceptance was performed by this local implementation session.
