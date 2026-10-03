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
  datacontenttype?: 'application/json';
  /**
   * @deprecated
   */
  data_content_type?: 'application/json';
  /**
   * JSON event payload.
   */
  data: any;
  /**
   * Required once a JSON Schema is registered for this source and type.
   */
  schemaversion?: string;
  /**
   * Optional tenancy assertion; must match the bearer account.
   */
  accountid?: string;
  /**
   * @deprecated
   */
  account_id?: string;
};

