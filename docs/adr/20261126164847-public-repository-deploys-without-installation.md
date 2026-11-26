# ADR-20261126164847 · Public GitHub repositories deploy without an installation

- **Status:** accepted
- **Date:** 2026-11-26
- **Scope:** `githubd` (new `StreamPublicSourceRef` RPC), then `apid`
  source-ref deploy and scan, the dashboard new-app wizard, and the CLI
- **Amends:** ADR-092 (headless source-ref deploy: source came only through
  the account's durable installation), ADR-20261101160300 (GitHub connector
  app bindings)
- **Checked against:** ADR-012 (githubd is the only outbound GitHub caller),
  ADR-316 (branch freshness before promotion), spec §11 (ownership is proved
  by GitHub, never by a request field)

## Context

1. The "Deploy on Gregale" button (faas-web #496) sends a visitor to the
   new-app wizard with `source=git&repo=gregale-dev/<starter>`. The wizard
   and apid accept only a repository that one of the account's GitHub App
   installations lists: `resolveRepositoryInstallation` answers 403
   `github_repo_not_accessible` otherwise. A visitor can install the App only
   on accounts they control, so nobody outside `gregale-dev` can deploy a
   Gregale starter, or any other public repository they do not own.
2. The restriction is about how the archive is fetched, not about trust.
   Any account can already clone a public repository and `gregale deploy`
   it as an upload, with the same size caps, secret scan and builder VM.
3. apid's public preflight (`pkg/preflight`) already reads public
   repositories, anonymously or with `FAAS_PREFLIGHT_GITHUB_TOKEN`, but only
   to report compatibility.

## Decision

1. **githubd gains `StreamPublicSourceRef(repo_full_name, ref,
   max_archive_bytes)`.** It streams the same `StreamSourceRefChunk` frames
   as `StreamSourceRef` and takes no account or installation.
2. **GitHub proves the repository is public on every call.**
   `GET /repos/{owner}/{name}` must answer 200 with `private: false`,
   `visibility` absent or `public`, and the requested full name (case
   folded). A private, missing or renamed repository gets the same 403
   `github_repo_not_accessible`, so the answer never discloses that a
   private repository exists. A renamed repository deploys under its new
   name, as ADR-20261101160300 amendment 2 already requires for bindings.
3. **The archive is pinned to a commit.** The ref resolves to a full commit
   SHA through the API and codeload is fetched by that SHA, exactly like the
   installation path, under the same per-plan `SourceTarballMaxMB` cap.
4. **The credential can only read public repositories.**
   `FAAS_GITHUB_PUBLIC_READ_TOKEN` (githubd, operator secret, optional) is a
   fine-grained token of a machine user whose repository access is "Public
   repositories (read-only)": 5,000 REST calls per hour, and no private
   repository is reachable with it. githubd never uses an installation
   token here: an installation can see its own private repositories (the
   `gregale-dev` installation sees `gregale-dev`'s), and GitHub does not
   document installation tokens reading public repositories outside the
   installation. Without the variable githubd reads anonymously, at 60 calls
   per hour per egress IP (about 30 deploys), which suits development and
   test only. A misconfigured token is still covered by decision 2, which
   refuses every repository GitHub reports as private.
5. **A public deploy has no branch intent.** `github_source_ref` and
   `github_installation_id` stay NULL (the existing
   `deployments_github_source_ref_pair_chk` holds, no migration), so
   ADR-316's freshness check does not apply, and there is no binding,
   webhook, preview or push-to-deploy. Redeploying is a new deploy of a ref.
   A customer who wants push-to-deploy forks the repository and connects
   GitHub.
6. **apid tries the installation path first.** Only when no installation of
   the account sees the repository (or the account has none) do
   `POST /v1/apps/{slug}/deployments/source-ref` and
   `POST /v1/projects/scan/source-ref` use the public path. A deploy with
   `expected_source` or `source_branch`, and a scan that names an
   installation, never fall back: they keep the installation answer. On the
   public path apid records `access: "public"` on the `deploy.source_ref`
   audit row and counts each public fetch (scan or deploy) against
   `PublicSourceFetchesPerHour` in `pkg/api/limits.go` (30 per account per
   hour on every plan). A repository githubd does not confirm public, and a
   githubd without the RPC, get the installation path's refusal unchanged.
   The dashboard wizard stops requiring a GitHub connection for a public
   repository and skips the bind.
7. **Released by a platform flag.** The `public_source_deploys` platform flag
   (account and plan rules, default off) gates the fallback in apid. Off for
   an account, apid answers exactly as before this ADR and never calls the
   public RPC. The flag stays off, and the feature stays out of the customer
   docs and the published error reference, until it has been tested on
   production; then it is released to every account and the docs follow.

## Compatibility

The RPC is additive. githubd's recovery decorator forwards it; a githubd
without it answers `Unimplemented`, which apid treats as "no public path" and
answers today's 403, so apid N beside githubd N-1 behaves as before. No
schema change.

## Rollout

1. ADR, githubd RPC and client, decorator forwarding, env contract, and the
   apid deploy and scan fallback behind `public_source_deploys` with its audit
   field and `PublicSourceFetchesPerHour` (this change).
2. Production test with the flag on for an operator account, then the flag
   for every account, customer docs (`docs/source-ref.md`) and the error
   reference entry.
3. faas-web wizard: public repositories without a GitHub connection.
4. CLI: `gregale deploy --repo owner/name` for public repositories.

## Consequences

- Any public GitHub repository, including every Gregale starter, deploys
  from the dashboard and the Deploy button.
- No new trust boundary: the same code could already be uploaded, and the
  same caps, secret scan and builder VM apply.
- Public deploys do not follow the repository; customers who want that fork
  it.
- All public reads share one GitHub budget; the per-account limit keeps one
  account from spending it.

## Rejected alternatives

- **Fork or generate from a template into the customer's account first:**
  needs Administration write on customer accounts and a second consent, and
  a customer who only wants to try a repository does not want a copy. It
  remains a later "fork to enable push-to-deploy" step.
- **Read public repositories with the `gregale-dev` installation token:** it
  sees `gregale-dev`'s private repositories, and its reach outside the
  installation is undocumented.
- **Fetch in apid, as preflight does:** githubd owns GitHub egress and its
  credentials (ADR-012).
- **Anonymous reads only:** 60 calls per hour per egress IP, shared by every
  customer.
