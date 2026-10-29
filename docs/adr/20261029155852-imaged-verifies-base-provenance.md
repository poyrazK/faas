# ADR-20261029155852: imaged verifies a platform base's build provenance before it builds it

- **Status:** accepted
- **Date:** 2026-10-29
- **Owner:** @poyrazK
- **Related:** issue #295; #5835 (images.yml attests every platform image it promotes on `main`); #5895 (release.yml verifies the digests it pins); ADR-736 (immutable runtime releases); ADR-20261024191249 (a rolled node stays drained until it can boot its bases)
- **Decision:** imaged checks the SLSA provenance attestation of every platform base digest before it builds that base's ext4. It runs the host's pinned cosign offline against a Sigstore trusted root shipped in the signed release. A mode setting (`off`, `observe`, `enforce`) controls what a failure does. The default is `observe`, which reports the failure and stages the base anyway.

## Context

#5835 made images.yml attest each image it promotes on `main` (`actions/attest-build-provenance`, `push-to-registry: true`). The attestation subject is the linux/amd64 platform manifest digest. #5895 makes release.yml refuse to pin a digest without a valid attestation from `images.yml` on `refs/heads/main`.

That gate covers only the digests a release pins, at the moment it pins them. It does not cover:

- a host whose `/etc/faas/runtime-bases.env` did not come from the signed release (a hand-edited join, a manual override);
- a base ref set outside that file (`FAAS_DEPLOY_BASE_REF_*`, the builder ref);
- a release cut from a workflow whose gate was removed or bypassed;
- a development node staging `:latest` tags.

imaged is where a base enters the fleet. Every production base ext4 is built in `ensureVerifiedBaseExt4` (`pkg/imaged/verified_base.go`): the builder base, minimal and runtime bases, execution profiles, assigned and release bases, and ADR-736 runtime releases. `ResolveImage` there is the one manifest fetch (`*oci.RegistryClient` always implements `oci.ImageResolver`, so the legacy `PullManifest` path never runs in production). Customer images never reach this function; they call `ResolveImage` directly.

Facts measured on 2026-10-29 that shape the decision:

- The pinned cosign (v2.4.1, `release.yml` `COSIGN_VERSION`, staged by node join at `/usr/local/bin/cosign`) cannot verify GitHub's bundles through `cosign verify-attestation <image>`. That command has no `--new-bundle-format` and no `--trusted-root` in 2.4.1. `cosign verify-blob-attestation --new-bundle-format --trusted-root … --offline <file>` works. On the rc.304 CLI archive and its GitHub attestation bundle, it printed `Verified OK`. With the wrong identity it failed with `expected SAN value …`, and with the wrong file with `unable to confirm artifact digest is present in subject digests`. It also ran with network access denied and an unwritable `HOME`, and wrote nothing.
- Attestations of a public repository are signed by public-good Sigstore (Fulcio `sigstore.dev`, Rekor). `gh attestation trusted-root` prints that root and GitHub's own; the bundle verified against the public-good one.
- Since #5835 a `sha-<commit>` tag is an index: the linux/amd64 manifest plus a buildx `attestation-manifest` entry. release.yml pins `crane digest --platform linux/amd64`, which is the attested child, and imaged's `resolved.Digest` is the same child. Digests pinned before #5835 (rc.304 and earlier) are single manifests with no attestation; rc.304's `runner-node22` digest returns 404 from the attestations API.
- GHCR has no referrers API: `/v2/<name>/referrers/<digest>` returns `404 MANIFEST_UNKNOWN`. It keeps the bundle under the referrers tag schema. `sha256-<hex>` is an index with one entry of artifact type `application/vnd.dev.sigstore.bundle.v0.3+json` (annotation `dev.sigstore.bundle.predicateType: https://slsa.dev/provenance/v1`), and that manifest's single layer is the bundle.
- The run for #5835 itself (images run 40897780922) attested every runtime base. On `runner-node22` linux/amd64 `sha256:df455b473706287c80fe92a1a5f3514c4b79c554898c6792b13ce47a7af892e7`, the whole chain in decisions 2–4 was checked with network access denied: fetch the bundle through the tag, check its blob digest, then hash the manifest bytes to the subject. cosign printed `Verified OK` for images.yml on `refs/heads/main`. It refused another workflow's identity and another manifest's bytes. The runner extension reads `github-hosted`.
- A base is rebuilt on every release even when its OCI digest is unchanged, because the guest-init digest is part of the reuse key.

## Decision

1. **Where.** The check runs in `ensureVerifiedBaseExt4` after `ResolveImage` and the second reuse check, and before any layer is pulled or the base is built. A base reused from its stored producer is not checked again; it was checked when it was built. A node that adopts another node's published base (`base_converge.go`, `refreshCachedBase`) does not check it again. In `enforce`, an unverified base is never built, so it is never published. The three manifest reads that don't build an ext4 (`handler.go`, `local_oci.go`, `runtime_default_base.go`) are out of scope.

2. **What.** The subject is `resolved.Digest`, the linux/amd64 platform manifest images.yml attests. The file handed to cosign is that manifest's raw bytes from the resolution evidence: `SelectedManifest` for an index, otherwise `SourceManifest`. `imagechain.Validate` already checks those bytes hash to `resolved.Digest`. cosign then requires the in-toto subject digest to equal the file's sha256, which proves the same thing `verify-attestation` would. The policy matches the release gate:
   - predicate type SLSA provenance v1 (`--type slsaprovenance1`);
   - certificate identity exactly `https://github.com/poyrazK/faas/.github/workflows/images.yml@refs/heads/main`;
   - issuer `https://token.actions.githubusercontent.com`;
   - the certificate's runner environment extension (OID `1.3.6.1.4.1.57264.1.11`) is `github-hosted`. imaged reads this itself after cosign passes, because cosign 2.4.1 has no flag for it. This matches the release gate's `--deny-self-hosted-runners`.

3. **Where the bundle comes from.** imaged fetches the bundle from the registry it pulls the base from, with the same credentials. It first tries the OCI 1.1 referrers API for `resolved.Digest`, filtered by artifact type `application/vnd.dev.sigstore.bundle.v0.3+json`. When the registry answers 404, it tries the referrers tag schema: the `sha256-<hex>` index that `push-to-registry` writes on registries without the API. This is where GHCR keeps it. imaged reads at most `BaseProvenanceMaxBundles` (8) bundles of at most `BaseProvenanceMaxBundleBytes` (1 MiB) each, both in `pkg/api/limits.go`, and stops at the first that verifies. The new reader lives in `pkg/oci` beside `PullImageSignatureAttachments`. It uses `PullBlobWithAuth`, which checks each blob's digest. imaged gets no new egress host and no GitHub API dependency.

4. **How.** imaged runs:

   ```
   cosign verify-blob-attestation --new-bundle-format --offline \
     --bundle <bundle> --trusted-root <root> --type slsaprovenance1 \
     --certificate-identity https://github.com/poyrazK/faas/.github/workflows/images.yml@refs/heads/main \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     <manifest file>
   ```

   - The binary is `FAAS_COSIGN_BIN`, default `/usr/local/bin/cosign`.
   - The exec follows the grype idiom (`runBoundedScanCommand`): bounded stdout and stderr, `WaitDelay`, no subprocess output echoed in errors, and a `BaseProvenanceVerifyTimeout` of 30 s in `pkg/api/limits.go`.
   - The environment is reduced to `PATH` and a `HOME` in imaged's private temp directory, where the bundle and manifest files also live (`PrivateTmp=yes`).
   - `--offline` checks the Rekor inclusion proof carried in the bundle against the Rekor key in the trusted root. imaged needs no egress to Sigstore.

5. **Trust root.** The release ships public-good Sigstore's `trusted_root.json` as `bin/sigstore-trusted-root.json`. It is catalogued with `vmlinux` in `pkg/releaseinstall/names.go` and hashed into the signed manifest's `tool_hashes`, the same way `mcp-bridged` joined the catalog in #5697. release.yml takes the root from `gh attestation trusted-root`, which fetches it through Sigstore's TUF repository. It keeps the entry whose certificate authority is `https://fulcio.sigstore.dev`. imaged reads the file beside its own executable, so the root it uses is always the one its own release shipped and it updates with every release.

6. **Verdict cache.** imaged persists each `verified` verdict per digest at `/srv/fc/sigs/base-provenance/sha256-<hex>.json`. The record holds the digest, identity, bundle sha256, trust root sha256, cosign version and time. imaged already writes `/srv/fc/sigs`. The per-release rebuild of an already verified digest reads the record and needs neither the registry nor cosign. Failures are never cached; the next stage tries again.

7. **Bases from before attestations.** ADR-736 runtime releases build the base ref each build recorded. A redeploy or rollback of a build made on a pre-#5835 base would otherwise be refused in `enforce`. The release ships a frozen list of the base digests that signed releases pinned before the first release with attested bases: `bin/base-provenance-legacy.txt`, catalogued like the root. Its source is `images/base-provenance-legacy.txt`, generated once by `scripts/base-provenance-legacy.sh --through v0.1.18-rc.304` from the `runtime-bases.env` asset of every release up to the last one cut before #5895's gate. It holds 243 digests and is never appended to. Builder-base digests are left out: imaged builds the builder base only from the ref its release configures, never from a ref a build recorded. A change to it needs an amendment to this ADR. A listed digest is reported as `legacy` and is staged in every mode. It is a genuine images.yml output from before images.yml attested anything, and the list admits nothing newer.

8. **Modes.** `FAAS_IMAGED_BASE_PROVENANCE` takes `off`, `observe` or `enforce`; unset means `observe`. Any other value stops imaged at startup. The compute and control-plane roles write it from the Ansible variable `faas_imaged_base_provenance_mode`.
   - `off`: no check.
   - `observe`: check, record and log, then build the base whatever the result.
   - `enforce`: a digest that is not `verified`, `cached` or `legacy` is not built. `ensureVerifiedBaseExt4` returns an error wrapping `ErrBaseProvenanceUnverified` with the ref and the result. Bases already staged keep serving. A node rolled onto a release whose bases it refuses stays drained under the existing activation gate (ADR-20261024191249).

9. **Signals.**
   - A counter `imaged_base_provenance_verifications_total{base,result}`, where `base` is the ref's repository name. `result` is one of:
     - `verified`
     - `cached`
     - `legacy`
     - `missing`: no bundle found
     - `rejected`: cosign refused every bundle, or the runner check failed
     - `unavailable`: no cosign or no trust root
     - `error`: a registry error or the timeout
   - One `slog` line per check with ref, digest, result and mode.
   - The alert `FaasBaseProvenanceUnverified` (severity `warn`, family `base_provenance`): `sum by (node, base, result) (increase(imaged_base_provenance_verifications_total{result!~"verified|cached|legacy"}[1h])) > 0`, with the runbook `docs/runbooks/FaasBaseProvenanceUnverified.md`.

10. **Operator and release check.** `imaged verify-base-provenance --ref <ref>` runs the same check without staging and prints the result. release.yml runs it, using the release's own imaged binary, cosign and shipped root, against every digest it pins, after #5895's gate. A release therefore cannot ship a root, a bundle location or a policy that its own imaged would reject.

## Rollout

1. The release ships the trust root and the legacy list, with their catalog entries.
2. `pkg/oci` gets the bundle reader, tested against a fake registry with the referrers API and with the tag schema.
3. imaged gets the verifier: cosign exec, runner check, verdict cache, legacy list, mode, metric, log, `verify-base-provenance` and the hook. The default is `observe`.
4. release.yml runs `imaged verify-base-provenance` on every pinned digest. This step also adds the alert, the runbook and the Ansible variable, and has the control-plane role stage the same pinned cosign as node join (until then those hosts report `unavailable`).
5. Turn on `enforce` on one node, then on the fleet. Do this only after a release with attested bases has rolled out, every node reports `verified`, `cached` or `legacy` for every base, and there has been no other result for 7 days. The owner decides when.

## Consequences

- A tampered or wrongly pinned base is reported (`observe`) or refused (`enforce`) wherever its ref came from, not only when a release pins it.
- In `enforce`, a GHCR outage or a missing bundle blocks building a digest this node has never verified. Already staged and already verified digests are unaffected. A blocked rollout holds the node drained instead of serving an unchecked base.
- Each new digest costs one extra registry round trip and one cosign exec (0.27 s measured offline, including hashing a 33 MB file), next to pulling hundreds of MiB of layers.
- A base ref pointing outside `poyrazK/faas` images.yml, for example a mirror that does not copy referrers, fails the check. `off` remains the escape hatch.
- The cosign version on hosts now matters to imaged as well as to release verification. A cosign upgrade can move to `verify-attestation --new-bundle-format` once both hosts and release.yml carry a version that supports it.
- Development nodes default to `observe` and usually have no cosign, so they report `unavailable` and keep working.

## Rejected alternatives

- **`cosign verify-attestation <image>`.** cosign 2.4.1 cannot read GitHub's bundle format with an offline root. Upgrading cosign on every host to use it changes the release verifier too, for no gain over the blob form.
- **sigstore-go inside imaged.** The owner chose the host's cosign: no new dependency tree in a daemon, and one tool for all of the fleet's Sigstore checks.
- **Online TUF (cosign's default).** imaged would need egress to the Sigstore CDN and would fail when the CDN is unreachable. The owner chose the root shipped in the release.
- **GitHub attestations API as the bundle source.** It would add `api.github.com` egress and the anonymous rate limit (60 requests per hour per address). The registry already serves the pull.
- **Enforce from the first release.** No release pins an attested base yet, and the fleet has never reported a result. The owner chose observe first.
- **Hook inside `oci.ResolveImage`.** Customer images also go through it.
