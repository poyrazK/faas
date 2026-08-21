/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for the informational `sidecar` form field on POST /v1/apps/{slug}/deployments/source-tarball (issue #961 / Mega-A PR-1, ADR-115). The CLI is the trust root for this deploy path; apid does NOT consult `github_installations` and does NOT attempt a server-side git fetch. The sidecar fields are recorded on the build row for provenance only — the build pipeline does NOT use them to fetch upstream.
 */
export type SourceTarballDeployRequest = {
  /**
   * `owner/repo` from the customer's git remote, parsed by `cmd/gregale/git_local.go::parseGitRemoteURL`. nil when the sidecar is omitted entirely.
   */
  repo?: string | null;
  /**
   * 40-char lowercase SHA from `git rev-parse HEAD`. Informational only; the build pipeline does NOT pin to this SHA.
   */
  ref?: string | null;
};

