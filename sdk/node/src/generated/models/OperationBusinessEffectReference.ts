/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Pair of retained Operation and milestone identifiers identifying a confirmed business effect.
 */
export type OperationBusinessEffectReference = {
  /**
   * Canonical nonzero UUID of the retained confirmed effect.
   */
  operation_id: string;
  /**
   * Canonical nonzero UUID of the milestone that recorded the retained confirmed effect.
   */
  milestone_id: string;
};

