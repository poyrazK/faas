# ADR-370 · GitHub Actions immutable OIDC subject bootstrap

- **Status:** accepted
- **Date:** 2026-09-28
- **Related:** ADR-310 (Git-driven deployment ownership and preview quotas)
- **Decision:** First-use account lookup accepts GitHub Actions subjects in both legacy `repo:OWNER/REPO:...` and immutable `repo:OWNER@OWNER_ID/REPO@REPO_ID:...` forms. It strips numeric IDs only to locate an existing OAuth-verified GitHub App repository binding. OIDC verification and the resulting trust policy continue to use the complete signed subject, including IDs.
- **Why:** GitHub now issues immutable owner and repository IDs in the default subject for new repositories and after repository rename or transfer. Treating those IDs as part of the repository name prevents a connected repository from bootstrapping its first Actions deploy token.
- **Consequences:** Existing repositories using the legacy subject remain compatible. Immutable subjects resolve only when both numeric IDs are present and the resulting owner/repository name matches one unambiguous connected binding. Invalid IDs and ambiguous bindings fail closed. The original subject remains exact in the auto-created OIDC trust policy; this lookup does not wildcard the trust.
- **Rejected alternatives:** Matching a wildcard subject or removing IDs before policy verification would broaden the trust boundary and discard GitHub's immutable identity. Requiring a long-lived API key would defeat the Actions OIDC path.
