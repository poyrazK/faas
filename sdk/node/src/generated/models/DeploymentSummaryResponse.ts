/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentChange } from './DeploymentChange.js';
import type { DeploymentResponse } from './DeploymentResponse.js';
/**
 * App-scoped release cockpit: selected deployment, immediate predecessor, non-secret field-level diff, and eligible rollback target.
 */
export type DeploymentSummaryResponse = {
  deployment: DeploymentResponse;
  /**
   * The immediately older deployment by created_at, or null for an initial release.
   */
  previous?: (DeploymentResponse | null);
  changes: Array<DeploymentChange>;
  /**
   * The latest superseded deployment eligible for POST /v1/apps/{slug}/rollback; omitted when none exists.
   */
  rollback_target_id?: string | null;
};

