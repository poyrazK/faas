/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Company queue counters and current plan limits across retained application namespaces.
 */
export type OrgQueueUsageResponse = {
  organization_id: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  bindings: number;
  pending: number;
  processing: number;
  dead_letter: number;
  /**
   * Reservations across all async invocation sources sharing company capacity.
   */
  reserved_async: number;
  max_async_invocations: number;
  max_triggers: number;
};

