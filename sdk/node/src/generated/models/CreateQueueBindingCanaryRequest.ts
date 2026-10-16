/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Publication consent and deployment selection for a bounded push queue canary.
 */
export type CreateQueueBindingCanaryRequest = {
  deployment_id: string;
  binding: string;
  ttl_seconds: number;
  publish_consent: boolean;
};

