/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppBindingInventory } from './AppBindingInventory.js';
import type { BindingReleasePolicy } from './BindingReleasePolicy.js';
import type { BindingVerificationQualification } from './BindingVerificationQualification.js';
import type { BindingVerificationRunStep } from './BindingVerificationRunStep.js';
/**
 * Public frozen review with no credentials, private probe pins or stdout. Status is an observation; traffic changes still require current atomic policy checks.
 */
export type BindingVerificationRun = {
  qualification?: BindingVerificationQualification;
  /**
   * Frozen shared admission budget; omitted values use two. Graph members share the root budget.
   */
  max_concurrency?: number;
  graph_run_id?: string;
  id: string;
  app: string;
  deployment_id: string;
  scope: string;
  status: 'pending' | 'passed' | 'blocked' | 'cancelled' | 'expired';
  reason?: string;
  created_at: string;
  expires_at: string;
  object_storage_write: boolean;
  queue_publish: boolean;
  review_digest: string;
  policy: BindingReleasePolicy;
  inventory: AppBindingInventory;
  steps: Array<BindingVerificationRunStep>;
};

