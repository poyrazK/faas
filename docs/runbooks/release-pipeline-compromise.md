# Release pipeline compromise (GitHub, CI, signing identity)

Use this when someone who should not have it may control what we build,
sign or deploy: a stolen maintainer GitHub session or token, a malicious
workflow change or third-party action, a poisoned dependency that ran in CI,
a leaked repository or environment secret, or an unexpected release tag.

This is SEV1. Follow [breach-notification.md](breach-notification.md) in
parallel for evidence, roles and notification deadlines.

## Why this is a fleet compromise

Production trusts GitHub completely:

- **Signing.** Releases are signed keyless with cosign. Hosts accept any
  artifact whose certificate identity is
  `https://github.com/poyrazK/faas/.github/workflows/release.yml@refs/tags/...`.
  Anyone who can push a `v*.*.*` tag, or change `release.yml` on a branch
  that gets tagged, produces artifacts the fleet accepts. Tag pushes are not
  protected.
- **Deploy.** Merges to `main` deploy automatically. The deploy freeze is a
  GitHub issue label, so an attacker with write access can lift it.
- **Secrets.** The `production` and `production-us` environments hold root
  SSH keys for the control plane and compute nodes (`CP_SSH_KEY`,
  `COMPUTE_SSH_KEY`), the fleet and every seal-domain age private key
  (`FLEET_AGE_KEY`, `SEAL_DOMAIN_*_AGE_KEY`), `COMPUTE_DATABASE_URL`, the
  Cloudflare DNS token (`EDGEDNSD_CF_TOKEN`), host signing and PKI material
  (`COMPUTE_SIGN_KEY`, `COMPUTE_PKI_TARBALL_B64`), storage envelopes and
  `NPM_TOKEN`. A workflow that can read an environment can read all of them.

Assume the attacker had every secret in any environment a compromised
workflow could reach, unless the GitHub audit log proves otherwise.

## 1. Contain (first 30 minutes)

Order matters: stop new code reaching hosts, then cut GitHub's access, then
rotate.

1. **Stop platform deploys from GitHub.** Open a `deploy-freeze` issue
   ([DeployFreeze.md](DeployFreeze.md)), then disable the CD workflows so a
   removed label cannot restart them:

   ```sh
   for wf in cd-platform.yml cd-controlplane.yml cd-compute.yml \
             cd-edge-node.yml cd-edge-units.yml cd-unit-watch.yml \
             release.yml release-units.yml; do
     gh workflow disable "$wf" --repo poyrazK/faas
   done
   ```

2. **Stop customer releases** while you work out whether builder images or
   the CLI were touched:

   ```sh
   sudo gregalectl break-glass pause --db-env /etc/faas/compute-db.env \
     --reason "SEV1 <ticket>: release pipeline compromise"
   ```

3. **Lock the attacker out of GitHub.** Revoke the suspected user's sessions,
   personal access tokens, SSH keys and OAuth app grants; remove any
   collaborator, deploy key or GitHub App installation you did not add;
   require two-factor for everyone. If the account is the repository owner's,
   do this from the owner's recovered account first.
4. **Cut GitHub's access to the fleet.** Remove the CD public keys from
   `authorized_keys` (or OS Login) on every host, and remove the workload
   identity bindings that let GitHub Actions impersonate GCP service
   accounts (`gcloud iam service-accounts get-iam-policy` on each service
   account the workflows authenticate as, for example
   `GCP_FLEET_BUNDLE_READER_SERVICE_ACCOUNT` in `cd-compute.yml`). Do this before
   rotating, so new secrets are not exposed through the same path.
5. Preserve evidence: export the GitHub audit log, the workflow run logs for
   the window, and the list of tags and releases with their creation times.

## 2. Scope

- **What ran?** List workflow runs in the window (`gh run list --repo
  poyrazK/faas --limit 200 --json name,headBranch,event,createdAt,actor`).
  Any run of a `cd-*`, `release*` or `fleet-enrollment` workflow by an
  unexpected actor, from an unexpected branch, or with a changed workflow
  file is a candidate.
- **What was signed?** List tags and releases created in the window. Every
  unexpected tag is a signed artifact the fleet would accept.
- **What deployed?** Compare the releases deployed in the window with the
  last release you trust: the `cd-*` run history, and the post-deploy record
  on the control plane (`/var/lib/faas/deploy/unit-watch.json`, see
  [DeployFreeze.md](DeployFreeze.md)). The record lives on a host the
  attacker may have reached, so trust the run history first.
- **Which secrets?** Every environment secret readable by any candidate run.

## 3. Eradicate

1. Delete unexpected tags and releases, and the packages they published to
   GHCR and npm (`npm deprecate` plus unpublish within the npm window).
2. Revert unexpected commits and workflow changes on `main` through a
   reviewed pull request.
3. If a malicious release reached any host, treat that host as compromised:
   drain it (`gregalectl compute-nodes drain`), rebuild it from a trusted
   release ([provider-neutral-node-join.md](provider-neutral-node-join.md)),
   and do not reuse its identities.

## 4. Rotate

Rotate every secret in scope, with new values generated off GitHub:

| Secret | How | Runbook |
|---|---|---|
| Fleet and seal-domain age keys | new identity, re-seal, retire the old one | [customer-secret-store-compromise.md](customer-secret-store-compromise.md) §3, [seal-domains-rollout.md](seal-domains-rollout.md) |
| Host signing keys (`COMPUTE_SIGN_KEY`, `COMPUTE_VERIFY_KEY`) | `gregalectl sign-keys rotate` on each host, then update the environment | |
| Internal PKI (`COMPUTE_PKI_TARBALL_B64`) | treat the fleet CA as exposed | [internal-ca-compromise.md](internal-ca-compromise.md) |
| SSH keys (`CP_SSH_KEY`, `COMPUTE_SSH_KEY`, `DO_SSH_KEY`) and `COMPUTE_KNOWN_HOSTS` | new keypair; old public key removed everywhere in step 1.4 | |
| `COMPUTE_DATABASE_URL` and other database credentials | rotate the role password | [postgres-runtime-roles.md](postgres-runtime-roles.md) |
| `EDGEDNSD_CF_TOKEN` | revoke in Cloudflare, issue a new token with the same narrow scope; review the zone's audit log | [dns-registrar-compromise.md](dns-registrar-compromise.md) |
| Storage envelopes, rclone config | rotate the storage credentials at the provider | [PostgresBackup.md](PostgresBackup.md) |
| `GATEWAYD_EDGE_CHALLENGE_KEY`, `FLEET_BUNDLE_AUTH_TOKEN`, `NPM_TOKEN` | regenerate and replace | |

If customer secrets could be decrypted with the exposed age keys, customers
must rotate their own values: follow customer-secret-store-compromise.md §3
and §4.

## 5. Resume

1. Update the environment secrets, re-add a CD public key and the workload
   identity bindings, and re-enable the workflows.
2. Cut a fresh release from a reviewed commit and deploy it to every host,
   so nothing still runs an artifact from the window.
3. Resume customer releases, close the `deploy-freeze` issue, and watch the
   post-deploy record ([DeployFreeze.md](DeployFreeze.md)):

   ```sh
   sudo gregalectl break-glass resume --db-env /etc/faas/compute-db.env \
     --reason "SEV1 <ticket>: pipeline contained"
   ```

## Before you need it

- [ ] Protect `v*.*.*` tags with a ruleset that only release maintainers can
      bypass, and require review for changes under `.github/workflows/`.
- [ ] Require approval on the `production` and `production-us` environments.
- [ ] Pin third-party actions to commit SHAs.
- [ ] Move the fleet and seal-domain private keys out of GitHub
      environments where the workflow only needs the public recipient.
- [ ] Every maintainer uses a hardware security key for GitHub.
