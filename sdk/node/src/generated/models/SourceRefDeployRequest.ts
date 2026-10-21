/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryPresetSpec } from './CanaryPresetSpec.js';
import type { DeploymentHealthcheck } from './DeploymentHealthcheck.js';
import type { SourceReviewResponse } from './SourceReviewResponse.js';
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
   * Reviewed native service handoff or worker generation activation. Requires expected_source, omitted traffic, an unprotected target and matching workload mode.
   */
  native_rollout?: 'service' | 'worker';
  /**
   * Commit-pinned staging or native rollout guard. Requires no_triggers, an exact commit, and no rollout overrides. Repository declarations are not imported. Named targets freeze reviewed settings; the response includes staged_review.
   */
  expected_source?: SourceReviewResponse;
  /**
   * Startup readiness for this source-ref deployment. Select exactly one HTTP path or standard gRPC health probe; omitted preserves source inference.
   */
  healthcheck?: DeploymentHealthcheck;
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
   * Optional branch provenance for a request whose `ref` is a full
   * commit SHA. The server verifies this branch still points at the
   * fetched commit, then rechecks it immediately before promotion.
   * GitHub Actions push deployments use this to keep their immutable
   * event SHA from becoming stale during a long build. Omit for an
   * explicit pinned deployment or a tag.
   *
   */
  source_branch?: string;
  /**
   * Forward-compat field. PR-A only supports `tarball`.
   */
  format?: 'tarball';
  /**
   * Registered project environment to target for this source-ref deployment.
   */
  environment?: string;
  /**
   * Skip applying trigger declarations from the fetched gregale.yaml; workflow definitions are still deployed.
   */
  no_triggers?: boolean;
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
  /**
   * Source-ref deployment opt-in for first-wake 5xx auto-rollback; Pro/Scale only, with omitted or null defaulting to false.
   */
  rollback_on_5xx?: boolean | null;
  /**
   * Source-ref deployment grant consumed once to permit cutover despite outstanding tasks requiring omitted handler versions.
   */
  mcp_task_migration_override?: boolean;
  /**
   * Release this source ref to production while a deploy freeze is active; the reason is audited (ADR-20261019071733).
   */
  override_freeze?: string;
  /**
   * Build this source ref now, keep it live without traffic, and start its production rollout at this time; deploy freezes are checked at that time (ADR-20261021152201).
   */
  release_at?: string;
  /**
   * Slack or email notification channels told when the scheduled release starts or cannot start; requires release_at.
   */
  release_notify?: Array<string>;
  /**
   * Opt this source-ref deployment out of temporary startup CPU headroom. Omitted or null preserves the default boost.
   */
  disable_startup_cpu_boost?: boolean | null;
};

