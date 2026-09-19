/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppResponse } from './AppResponse.js';
import type { QueueBindingResponse } from './QueueBindingResponse.js';
import type { ScalingPolicy } from './ScalingPolicy.js';
/**
 * The converged queue binding and queue-depth scaling policy.
 */
export type QueueWorkloadProfileResponse = {
  app: AppResponse;
  binding: QueueBindingResponse;
  scaling_policy: ScalingPolicy;
  /**
   * True when the default binding was created by this request.
   */
  created: boolean;
};

