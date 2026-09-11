/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryPresetSpec } from './CanaryPresetSpec.js';
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
  /**
   * Free-form operator note on the tarball deploy request (≤280 chars). Example: 'Emergency rollback after payment provider incident'.
   */
  reason?: string;
  /**
   * Closed-set annotation tag on the tarball deploy request for grouping/filtering.
   */
  tag?: 'incident_recovery' | 'hotfix' | 'scheduled_maintenance' | 'compliance_hold' | 'partner_request';
  /**
   * Human-readable actor label on the tarball deploy request. CLI auto-captures from `git config user.name`; githubd stamps pusher.name; the GitHub Action defaults to ${{ github.actor }}.
   */
  deployed_by?: string;
  /**
   * Pull-request number that drove this tarball deploy request (githubd pull_request.number; Action ${{ github.event.pull_request.number }}). NULL for push-to-main with no inferred PR.
   */
  pr_number?: number;
  /**
   * Explicit initial traffic weight for this source-tarball deployment. Omitted uses the normal stable rollout; zero stages a dark live revision.
   */
  traffic_percent?: number | null;
  /**
   * Canary rollout policy for this source-tarball deployment. Mutually exclusive with traffic_percent.
   */
  canary?: (CanaryPresetSpec | null);
};

