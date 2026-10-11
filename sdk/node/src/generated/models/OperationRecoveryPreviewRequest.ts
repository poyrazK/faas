/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Proposed recovery resolution without a durable decision identity or reconciliation evidence.
 */
export type OperationRecoveryPreviewRequest = {
  expected_generation: number;
  resolution: 'succeeded' | 'failed' | 'cancelled' | 'safe_to_retry';
  /**
   * Proposed pinned-schema output required only for a succeeded preview.
   */
  result?: any;
};

