# Supply chain

This page covers what Gregale signs, attests and verifies about the software it
ships (issue #295), and how to check each artifact yourself. It is the answer to
the SBOM, signing and build provenance questions in security questionnaires
such as CAIQ and SIG.

## What each artifact carries

| Artifact | Signature and provenance | SBOM |
| --- | --- | --- |
| Daemon release bundle `release.tar.gz` (daemons, guest init, function runners, guest kernel, Sigstore trusted root) | Keyless cosign signature (`release.cosign.bundle`) and an SLSA provenance attestation | `release.sbom.json` (SPDX), a release asset |
| CLI archives `gregale_<version>_<os>_<arch>.tar.gz` | SLSA provenance attestation | SPDX SBOM attestation covering every archive's binary |
| Platform images on ghcr: builder-base (linux/amd64 and linux/arm64) and every runtime base (linux/amd64) | SLSA provenance attestation, pushed to ghcr beside the image | SPDX SBOM attestation, pushed beside the image |

All signing is keyless. Each signature comes from a short-lived Sigstore
certificate issued to the GitHub Actions workflow that built the artifact, and
is recorded in the public Rekor transparency log. No long-lived signing key
exists that could leak. The signing identities are:

- **Platform images:** `https://github.com/poyrazK/faas/.github/workflows/images.yml@refs/heads/main`, on a GitHub-hosted runner.
- **Release bundle and CLI archives:** `https://github.com/poyrazK/faas/.github/workflows/release.yml@refs/tags/<tag>`.

Provenance uses the SLSA v1 predicate (`https://slsa.dev/provenance/v1`).
GitHub documents artifact attestations as meeting SLSA v1.0 Build Level 2.

## Verify an artifact

Use the GitHub CLI (`gh` 2.50 or later).

```bash
# A platform image. The digest is the linux/amd64 (or linux/arm64) image a
# release pins in runtime-bases.env.
gh attestation verify oci://ghcr.io/poyrazk/runner-node22@sha256:<digest> --owner poyrazK \
  --signer-workflow poyrazK/faas/.github/workflows/images.yml --source-ref refs/heads/main \
  --deny-self-hosted-runners
gh attestation verify oci://ghcr.io/poyrazk/runner-node22@sha256:<digest> --owner poyrazK \
  --predicate-type https://spdx.dev/Document/v2.3

# A CLI archive.
gh attestation verify gregale_<version>_linux_amd64.tar.gz -R poyrazK/faas
gh attestation verify gregale_<version>_linux_amd64.tar.gz -R poyrazK/faas \
  --predicate-type https://spdx.dev/Document/v2.3

# The daemon release bundle: provenance, and the cosign signature hosts check.
gh attestation verify release.tar.gz -R poyrazK/faas
cosign verify-blob --bundle release.cosign.bundle \
  --certificate-identity-regexp '^https://github\.com/poyrazK/faas/\.github/workflows/release\.yml@refs/tags/.+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  release.tar.gz
```

To save an attested SBOM as a file, take the verified statement's predicate:

```bash
gh attestation verify gregale_<version>_linux_amd64.tar.gz -R poyrazK/faas \
  --predicate-type https://spdx.dev/Document/v2.3 \
  --format json --jq '.[0].verificationResult.statement.predicate' > gregale-cli.spdx.json
```

## Where Gregale verifies

1. **When a release is cut.** release.yml refuses to pin a platform image digest
   that has no valid provenance from images.yml on `main` (#5895). Before it
   signs, it runs the imaged binary it just packed, with the shipped Sigstore
   root, against every digest it pins (ADR-20261029155852).
2. **When a host installs a release.** The deploy workflows check the release
   checksum, the cosign identity, the embedded release manifest and the
   production manifest hash before they stage anything on a host.
3. **When a node builds a platform base.** imaged checks the base digest's
   provenance with the host's pinned cosign, offline, before it builds the base
   (ADR-20261029155852). `FAAS_IMAGED_BASE_PROVENANCE=observe`, the default,
   reports failures through `FaasBaseProvenanceUnverified`. `enforce` refuses
   the base.

## SBOMs for a local build

`make sbom` writes CycloneDX SBOMs into `sbom/`. One covers the binaries in
`bin/` (run `make build` first). Each image reference in `SBOM_IMAGES` gets its
own:

```bash
make sbom SBOM_IMAGES="ghcr.io/poyrazk/runner-node22@sha256:<digest>"
```

It needs Syft on the PATH. CI uses Syft 1.49.0, pinned by checksum in
`scripts/ci/install-syft.sh`.

## Not covered

- **Reproducible builds.** Out of scope for #295.
- **Customer images.** The platform does not require provenance for images
  customers deploy. An app can require signed images from its own trusted
  publishers instead (see "App deploy guard" in `docs/security.md`).
- **Changes from issue #295's original plan.** It proposed key-based
  `cosign sign --key` with a published `cosign.pub`, and `slsa-github-generator`
  output in `docs/security/`. Keyless Sigstore signatures and GitHub artifact
  attestations replaced both. They prove the same build provenance without a
  signing key to protect, and they live with each artifact instead of in this
  repository.
