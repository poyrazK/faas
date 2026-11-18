/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded producer return classification. Capacity and lease loss are separate from operation errors. Success refers to a returned call, not a customer command outcome. Capacity series can be absent until first observed and must not be synthesized as zero.
 */
export type ObsRuntimeAppTaskOperationOutcome = 'success' | 'error' | 'canceled' | 'lease_lost' | 'empty' | 'cancellation_pending' | 'capacity';
