/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckReport } from './BindingCheckReport.js';
import type { DeploymentResponse } from './DeploymentResponse.js';
/**
 * Atomic traffic transition and the bindings check enforced at its write boundary.
 */
export type BindingPromotionResponse = {
  deployment: DeploymentResponse;
  from_percent: number;
  to_percent: number;
  already_promoted: boolean;
  bindings_check: BindingCheckReport;
};

