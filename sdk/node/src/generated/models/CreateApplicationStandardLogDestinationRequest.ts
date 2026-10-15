/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A logging endpoint and optional write-only credential to seal.
 */
export type CreateApplicationStandardLogDestinationRequest = {
  name: string;
  kind: 'http_json' | 'otlp';
  /**
   * HTTPS endpoint without userinfo, query string or fragment. Put credentials in auth_header.
   */
  target_url: string;
  /**
   * One Name/value header, sealed server-side and never returned.
   */
  auth_header?: string;
};

