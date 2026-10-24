/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckReport } from './BindingCheckReport.js';
/**
 * Committed exact canary abort, including the restored traffic recipient and any binding reports required by its stored release policy.
 */
export type RolloutRecoveryReceipt = {
  deployment_id: string;
  predecessor_deployment_id: string;
  restored_traffic_percent: 100;
  bindings_checks?: Array<BindingCheckReport>;
};

