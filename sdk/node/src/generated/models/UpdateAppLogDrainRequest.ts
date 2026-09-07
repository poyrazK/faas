/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partially update a runtime log destination; omitted fields remain unchanged.
 */
export type UpdateAppLogDrainRequest = {
  kind?: 'http_json' | 'otlp';
  target_url?: string;
  /**
   * One Name: value pair; an empty value clears credentials.
   */
  auth_header?: string;
  enabled?: boolean;
};
