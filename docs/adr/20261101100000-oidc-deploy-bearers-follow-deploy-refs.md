# ADR-20261101100000: OIDC deploy bearers follow the repository's deploy refs and bind to one app

- **Status:** accepted
- **Date:** 2026-11-01
- **Amends:** ADR-360 (OIDC exchange admits every subject of an OAuth-bound repository), ADR-101 (secretless deploys), ADR-20261030070000 (draft previews)
- **Context:** ADR-360 lets a trust policy Gregale created admit every workflow of a repository bound to the account, so another branch, a `pull_request` run or a second repository could deploy. Every such exchange minted an account-wide `deploy:write` bearer; the exchange's `app` field was only recorded for audit, and nothing downstream looked at the token's repository or ref. A `pull_request` run of a same-repository pull request executes the workflow file from the pull request itself, so anyone who could open a pull request could deploy its unreviewed code to production of any app in the account, past required reviews and branch protection. The GitHub App's push-to-deploy never did that: it deploys production only from the production branch, and project environments only from their deploy branches.
- **Decision:**
  1. Under a trust policy Gregale created, a deploy bearer is issued only to a run whose `ref` claim is a branch Gregale deploys from that repository (the production branch of a binding or project, or a project deploy branch) or a `v*` release tag (what the workflow `gregale github setup` writes deploys), and whose `event_name` is not `pull_request` or `pull_request_target`.
  2. The exchange must name the app (`app` in the JSON body, or `resource=urn:gregale:app:<slug>` in the RFC 8693 form), and that app must be bound to the token's repository (its own GitHub binding, or its project's). The bearer is bound to that app: the auth middleware confines it like a per-app deploy token (the app's `/v1/apps/{slug}` routes, the upload routes and its deployment reads), and `appaccess` refuses any other app.
  3. A refused exchange writes nothing: on first use no trust policy is created.
  4. A trust policy the account wrote is authoritative, as before (bug hunt 2026-10-28 H46): its bearers stay account-wide from whatever subjects it admits. The environment-preflight capability, which cannot deploy, is not gated.
  5. With draft previews on, `ready_for_review` at the head a live preview already serves renews the preview's lease and reports it live instead of rebuilding every workload.
- **Why:** Code review is the gate between a pull request and production. A credential any pull request can mint must not deploy what review has not passed, and a token from one repository must not deploy another repository's app.
- **Consequences:**
  - One expand migration adds a nullable `oidc_exchanged_tokens.app_id`. A bearer from an older apid has none and stays account-wide until it expires (an hour at most).
  - CI that deployed from other branches, pull requests or tags outside `v*` through a policy Gregale created now gets 403 with the allowed refs. Such a workflow needs a trust policy the account writes for it.
  - A `v*` tag deploys from whatever commit it points at; teams that rely on review should protect release tags (GitHub tag rulesets) as they protect the production branch.
- **Rejected alternatives:**
  - **Issue preview-only bearers to pull request runs.** PR previews are built by the GitHub App from the pull request event; a CI bearer adds nothing but a second, weaker path.
  - **Leave bearers account-wide and only gate the ref.** A reviewed workflow of one repository could still deploy another repository's app.
  - **Gate on the subject instead of the `ref` claim.** A job with `environment:` gets an environment subject that carries no ref, and GitHub lets a workflow name a new, unprotected environment.
