/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckBindingResult } from './BindingCheckBindingResult.js';
import type { BindingCheckFinding } from './BindingCheckFinding.js';
import type { BindingInventoryIssue } from './BindingInventoryIssue.js';
import type { BindingRuntimeDeployment } from './BindingRuntimeDeployment.js';
/**
 * Safe preflight findings for the declared policy at checked_at. Coverage may be complete, partial or none; a passed report does not independently establish application readiness or credential use. Optional application acknowledgements are version-bound self-attestations.
 */
export type BindingCheckReport = {
  expected_deployment_id?: string;
  app: string;
  scope: string;
  deployment_id: string;
  checked_at: string;
  inventory_generated_at: string;
  max_verification_age: string;
  allow_unsupported: boolean;
  /**
   * Whether this check required application self-attestations for current managed PostgreSQL/object-storage secrets.
   */
  require_application_ack?: boolean;
  passed: boolean;
  coverage: string;
  bindings: Array<BindingCheckBindingResult>;
  runtime: Array<BindingRuntimeDeployment>;
  issues: Array<BindingInventoryIssue>;
  blockers: Array<BindingCheckFinding>;
  warnings: Array<BindingCheckFinding>;
};

