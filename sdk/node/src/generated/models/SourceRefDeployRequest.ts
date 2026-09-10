/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryPresetSpec } from './CanaryPresetSpec.js';
/**
 * JSON body for POST /v1/apps/{slug}/deployments/source-ref
 * (DEPLOY-PROV-4 / ADR-092, issue #739). The headless CI deploy
 * path: `repo` resolves to an install-token-bound fetch, `ref` is
 * the customer's chosen input (branch / tag / SHA — server
 * resolves to a 40-char SHA before stamping the deployment row).
 *
 */
export type SourceRefDeployRequest = {
  /**
   * GitHub owner/name slug, e.g. `onebox-faas/hello`.
   */
  repo: string;
  /**
   * Commit ref — 40-char SHA, branch, or tag. api.github.com
   * /repos/<repo>/commits/<ref> resolves branch / tag inputs
   * to a pinned SHA before the tarball fetch starts. The wire
   * shape pins to the resolved 40-char SHA (server override;
   * caller's `ref` is preserved on the `deploy.source_ref`
   * audit row for traceability).
   *
   */
  ref: string;
  /**
   * Forward-compat field. PR-A only supports `tarball`.
   */
  format?: 'tarball';
  /**
   * Free-form operator note (≤280 chars). Example: 'Emergency rollback after payment provider incident'.
   */
  reason?: string;
  /**
   * Closed-set annotation tag for grouping/filtering.
   */
  tag?: 'incident_recovery' | 'hotfix' | 'scheduled_maintenance' | 'compliance_hold' | 'partner_request';
  /**
   * Human-readable actor label. CLI auto-captures from `git config user.name`; githubd stamps pusher.name; the GitHub Action defaults to ${{ github.actor }}.
   */
  deployed_by?: string;
  /**
   * Pull-request number when the wire offers it (githubd pull_request.number; Action ${{ github.event.pull_request.number }}). NULL for push-to-main with no inferred PR.
   */
  pr_number?: number;
  /**
   * Explicit initial traffic weight for this source-ref deployment. Omitted uses the normal stable rollout; zero stages a dark live revision.
   */
  traffic_percent?: number | null;
  /**
   * Canary rollout policy for this source-ref deployment. Mutually exclusive with traffic_percent.
   */
  canary?: (CanaryPresetSpec | null);
};

