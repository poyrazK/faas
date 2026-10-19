/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Producer-key event publication with a 1 MiB total body limit.
 */
export type AppPublishEventRequest = {
  /**
   * Optional canonical application UUID guard; mismatches return 409 before publication or comparison.
   */
  expected_app_id?: string;
  /**
   * Stable exact application-scoped producer key; preserve on retry.
   */
  key: string;
  type: string;
  /**
   * Event data associated with this producer key.
   */
  data: any;
  /**
   * Original occurrence time; defaults to ingress time.
   */
  time?: string;
  schemaversion?: string;
};

