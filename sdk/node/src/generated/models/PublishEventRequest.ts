/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Caller-authored envelope for the tenant-scoped internal event router.
 */
export type PublishEventRequest = {
  /**
   * Caller-chosen idempotent event identifier.
   */
  id: string;
  /**
   * Logical producer or service that emitted the event.
   */
  source: string;
  /**
   * Event type used by future content-based matching.
   */
  type: string;
  /**
   * Event occurrence time; omitted values are stamped at ingress.
   */
  time?: string;
  data_content_type?: 'application/json';
  /**
   * JSON event payload.
   */
  data: any;
  /**
   * Optional tenancy assertion; must match the bearer account.
   */
  account_id?: string;
};

