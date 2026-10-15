/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Payload-free identity of an expired or model-at-risk sampled recipient. Absence from the sample is not evidence that its deadline is safe.
 */
export type EventSubscriptionRateDeadlineRisk = {
  event_source: string;
  event_id: string;
  accepted_at: string;
  deadline_at: string;
  modeled_admission_at?: string;
  reason: 'already_expired' | 'modeled_deadline_risk';
};

