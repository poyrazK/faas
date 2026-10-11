/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRoutingRetryPolicy } from './EventRoutingRetryPolicy.js';
/**
 * Configured routing retry policy for an application event subscription, or its default policy.
 */
export type EventRoutingRetryPolicyResponse = {
  subscription_id: string;
  configured: boolean;
  policy: EventRoutingRetryPolicy;
};

