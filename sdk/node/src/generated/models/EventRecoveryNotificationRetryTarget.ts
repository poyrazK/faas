/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact receiver selection and expected delivery generation copied from a recovery notification retry preview. Repeated delivery IDs are rejected.
 */
export type EventRecoveryNotificationRetryTarget = {
  kind: 'admission' | 'execution';
  webhook_id: string;
  delivery_id: string;
  expected_replay_generation: number;
};

