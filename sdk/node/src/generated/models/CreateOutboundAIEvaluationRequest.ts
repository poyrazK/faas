/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PutOutboundAIRouteCanaryRequest } from './PutOutboundAIRouteCanaryRequest.js';
/**
 * Evaluate a route target against the route's first target. Omitted settings take their defaults: a sample of 20 and a max_loss_rate of 20. canary, when set, must name the same target and starts if the evaluation passes.
 */
export type CreateOutboundAIEvaluationRequest = {
  /**
   * The 1-based route position evaluated.
   */
  target: number;
  /**
   * One of the account's enabled AI integrations.
   */
  judge_integration_id: string;
  judge_model: string;
  sample_size?: number;
  max_loss_rate?: number;
  canary?: PutOutboundAIRouteCanaryRequest;
};

