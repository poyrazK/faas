/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Problem } from './Problem.js';
import type { PublishEventResponse } from './PublishEventResponse.js';
/**
 * Durable acceptance or failure observation for one batch input position.
 */
export type PublishEventBatchResult = {
  /**
   * Zero-based input position.
   */
  index: number;
  status: 'accepted' | 'duplicate' | 'rejected' | 'unknown';
  /**
   * Retry using exactly the original event identity and content.
   */
  retryable: boolean;
  receipt?: PublishEventResponse;
  problem?: Problem;
};

