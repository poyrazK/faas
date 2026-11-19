# Release manifest materialization

The signed daemon bundle must be tied to the exact deployment manifest that
is rendered and applied to the production fleet. The checked-in production
file is now a topology template:

```text
deploy/manifest/production/gcp-live.template.yaml
```

The template a release uses is the one file name in
`deploy/manifest/production/release-template`, resolved by
`scripts/release-template.sh`. It is checked in, not a CI variable, so the
tag commit decides which fleet the release targets. The materializer refuses
a template that carries a credential (`scripts/check-manifest-credentials.sh`),
because `production-manifest.yaml` is published with the release.

It deliberately contains a schema-valid placeholder release identity. The
release workflow materializes the final manifest using the tagged commit,
computes its SHA-256, embeds that hash in `release-manifest.json`, and
publishes the exact YAML as `production-manifest.yaml` beside the signed
bundle.

This is automatic and avoids the impossible circular operation of committing
a file containing its own commit SHA. It also removes the mutable
`GREGALE_RELEASE_MANIFEST_HASH` repository variable from the release gate.

For a local pre-release check:

```sh
RUNTIME_BASES_ENV=/secure/runtime-bases.env ./scripts/pre-release-check.sh
```

For an explicit materialization:

```sh
scripts/materialize-release-manifest.sh \
  --git-sha "$(git rev-parse HEAD)" \
  --runtime-bases-env /secure/runtime-bases.env \
  --output /tmp/production-manifest.yaml
```

The runtime contract must contain one immutable `@sha256:` OCI reference for
each supported runtime. Release CI generates this file from the successful
`images.yml` publisher run; local checks must provide an equivalent file.

Never use the illustrative manifest under `deploy/manifest/examples/` or a
temporary `live-e2e-*` release as a production release anchor. Operators
deploy the `production-manifest.yaml` asset emitted by the same signed
release that contains the daemon bundle.
