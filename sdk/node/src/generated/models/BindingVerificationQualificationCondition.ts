/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Temporary readiness condition preventing final qualification of a selected binding.
 */
export type BindingVerificationQualificationCondition = {
  app: string;
  code: 'runtime_updating' | 'runtime_stale' | 'queue_consumer_unobserved' | 'queue_consumer_stale' | 'application_adoption_unknown' | 'application_ack_stale' | 'application_ack_candidate_unobserved';
  type?: string;
  name?: string;
  binding?: string;
  scope?: string;
};

