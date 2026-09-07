/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a provider-neutral runtime log destination.
 */
export type CreateAppLogDrainRequest = {
  kind: 'http_json' | 'otlp';
  target_url: string;
  /**
   * One Name: value pair; sealed at rest and never returned.
   */
  auth_header?: string;
  enabled?: boolean;
};
