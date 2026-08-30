/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryPresetSpec } from './CanaryPresetSpec.js';
import type { CreateDeploymentOverrides } from './CreateDeploymentOverrides.js';
import type { Sidecar } from './Sidecar.js';
/**
 * Two content-types accepted (see operation description): prebuilt OCI image reference, or multipart source upload. The optional `overrides` object (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a different entrypoint / cmd / env / env_secrets / port / healthcheck without rebuilding the image. The override field list is FROZEN — six fields, no more — and any extra field on the override object 400s the request (the handler's decoder rejects unknown keys; see ADR-053 §Decision 1). The optional `sidecars` array (issue #463 / ADR-068) attaches up to 2 stateless sidecars (1 init + 1 sidecar) per app — a one-shot DB migrator as `init`, a metrics scraper as `sidecar`. nil/omitted = no sidecars.
 */
export type CreateDeploymentRequest = {
  /**
   * registry.gregale.dev/...@sha256:... — digest-pinned OCI reference.
   */
  image?: string;
  /**
   * Deploy-time overrides (entrypoint, cmd, env, env_secrets, port, healthcheck). nil/omitted = deploy the image as-is.
   */
  overrides?: (CreateDeploymentOverrides | null);
  /**
   * Per-deploy signature-enforcement opt-in (issue #472 / ADR-054). nil = inherit apps.require_signed; *true is a no-op when the app flag is already on; *false is rejected with 403 deploy_signature_invalid when the app flag is on (operator policy wins).
   */
  require_signed?: boolean | null;
  /**
   * Up to 2 stateless sidecars (1 init + 1 sidecar). nil/omitted = no sidecars. See ADR-068 for the hard 2-cap and stateless-only contract.
   */
  sidecars?: Array<Sidecar>;
  /**
   * Per-deployment traffic-split weight (issue #556 PR-A). nil = server default 100; explicit 0..100 = opt into canary (Pro/Scale only).
   */
  traffic_percent?: number | null;
  /**
   * Top-level per-deployment env scope (ADR-091 / PR-D). Lowercase alnum + dash, 3..40 chars, no leading/trailing dash. nil/omitted = `default`.
   */
  scope?: string | null;
  /**
   * Free-form operator note (issue #977 / ADR-116). DB CHECK enforces length(reason) <= 280.
   */
  reason?: string | null;
  /**
   * Closed-set annotation tag. DB CHECK (deployments_tag_set_chk) enforces the same vocabulary.
   */
  tag?: 'incident_recovery' | 'hotfix' | 'scheduled_maintenance' | 'compliance_hold' | 'partner_request';
  /**
   * Operator label. CLI auto-captures from `git config user.name`; githubd stamps pusher.name; Action defaults to ${{ github.actor }}.
   */
  deployed_by?: string | null;
  /**
   * PR number (when known). 0 / NULL collapses to NULL on the row (DB CHECK rejects 0).
   */
  pr_number?: number | null;
  /**
   * Per-deployment auto-rollback opt-in (issue #961 leaf 8 / ADR-118 / Mega-C PR-2). Pro+ only. nil = server default false.
   */
  rollback_on_5xx?: boolean | null;
  /**
   * Per-deployment canary ladder (issue #976 / ADR-122 / SAFE-RELEASES-A). nil/omitted = server default 'none'. For preset='custom', stages carries the customer ladder.
   */
  canary?: (CanaryPresetSpec | null);
  /**
   * Per-deployment opt-in for the auto-fallback path (issue #1186 / ADR-141). nil = handler reads api.FullRootfsAllowAutoDefault[acct.Plan] (Free:false, Hobby+:true) and writes that onto the row.
   */
  full_rootfs_allow_auto?: boolean | null;
  /**
   * Tri-state per-deployment override (issue #1186 / ADR-141). NULL = honor allow_auto + plan gate. true = force full-rootfs (Free-plan override). false = force today-equivalent failure (Hobby+ opt-out). nil/omitted = inherit auto + plan.
   */
  full_rootfs_override?: boolean | null;
};

