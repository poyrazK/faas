/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryPresetSpec } from './CanaryPresetSpec.js';
import type { CreateDeploymentOverrides } from './CreateDeploymentOverrides.js';
import type { Sidecar } from './Sidecar.js';
import type { WorkflowSpec } from './WorkflowSpec.js';
/**
 * Two content-types accepted (see operation description): prebuilt OCI image reference, or multipart source upload. The optional `overrides` object (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a different entrypoint / cmd / env / env_secrets / port / healthcheck without rebuilding the image. The optional `companions` array attaches bounded helper workloads such as an OpenTelemetry collector, database proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing clients.
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
   * Per-deploy signature-enforcement opt-in (issue #472 / ADR-054). nil = inherit the app's effective signature policy; *true is a no-op when enforcement is already on; *false is rejected with 403 deploy_signature_invalid when apps.require_signed is on or security_policy=enforce (operator policy wins).
   */
  require_signed?: boolean | null;
  /**
   * Preferred field. Up to 2 stateless companions; managed presets may omit image. Do not set together with sidecars.
   */
  companions?: Array<Sidecar>;
  /**
   * Deprecated spelling of companions. Do not set both fields.
   * @deprecated
   */
  sidecars?: Array<Sidecar>;
  /**
   * Workflow DAG definitions for this deployment. Paid-plan only; persisted with the deployment and snapshotted at run start.
   */
  workflows?: Array<WorkflowSpec>;
  /**
   * Per-deployment traffic-split weight (issue #556 PR-A). nil = server default 100; explicit 0..100 = opt into canary (Pro/Scale only).
   */
  traffic_percent?: number | null;
  /**
   * Top-level per-deployment env scope (ADR-091 / PR-D). Lowercase alnum + dash, 3..40 chars, no leading/trailing dash. nil/omitted = `default`.
   */
  scope?: string | null;
  /**
   * Registered project environment to resolve to the deployment scope. Requires the app to belong to the project; omitted preserves legacy scope behavior.
   */
  environment?: string;
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
   * Per-deployment canary ladder (issue #976 / ADR-122 / SAFE-RELEASES-A). nil/omitted = server default 'none'. For preset='custom', stages carries the customer ladder.
   */
  canary?: (CanaryPresetSpec | null);
  /**
   * Create-time opt-in for first-wake 5xx auto-rollback; Pro/Scale only, with omitted or null defaulting to false.
   */
  rollback_on_5xx?: boolean | null;
  /**
   * Whether to auto-fallback to a self-contained rootfs for images without a Gregale runtime base. Omitted uses the plan default.
   */
  full_rootfs_allow_auto?: boolean | null;
  /**
   * Tri-state full-rootfs override: null uses the plan default, true forces full-rootfs, false forces the shared-base path.
   */
  full_rootfs_override?: boolean | null;
};

