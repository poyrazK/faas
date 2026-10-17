/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppBindingInventory } from './AppBindingInventory.js';
import type { BindingCheckFinding } from './BindingCheckFinding.js';
import type { BindingCheckReport } from './BindingCheckReport.js';
import type { BindingReleasePolicy } from './BindingReleasePolicy.js';
import type { BindingVerificationPlanStep } from './BindingVerificationPlanStep.js';
/**
 * Read-only application binding plan used to review selection and consent before receipt creation.
 */
export type BindingVerificationReview = {
  app: string;
  deployment_id: string;
  scope: string;
  service_targets: Record<string, string>;
  reviewed_at: string;
  ready: boolean;
  review_digest: string;
  object_storage_write_required: boolean;
  queue_publish_required: boolean;
  inventory: AppBindingInventory;
  policy: BindingReleasePolicy;
  plan: Array<BindingVerificationPlanStep>;
  blockers: Array<BindingCheckFinding>;
  observed_check?: BindingCheckReport;
};

