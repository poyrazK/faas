/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One observed receiver with its current generation and eligibility. Missing delivery identity prevents selecting this receiver for retry.
 */
export type EventRecoveryNotificationRetryCandidate = {
  kind: 'admission' | 'execution' | 'attention';
  webhook_id: string;
  delivery_id?: string;
  replay_generation: number;
  status: string;
  eligible: boolean;
  reason?: string;
};

