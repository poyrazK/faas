/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskOperationName } from './ObsRuntimeAppTaskOperationName.js';
/**
 * Coverage for one ordered operation across all inspected targets and seven outcomes. Category fields count signals with a positive observed delta, not delta magnitudes. Error returns, cancellation, lease loss and capacity refusal remain distinct. Execute success may include a customer command verdict and does not establish successful delivery or completion.
 */
export type ObsRuntimeAppTaskOperationSummary = {
  operation: ObsRuntimeAppTaskOperationName;
  increased: number;
  unchanged: number;
  baseline: number;
  unknown: number;
  error_increases: number;
  cancellations: number;
  lease_losses: number;
  capacity_refusals: number;
};

