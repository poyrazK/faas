/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Resolved immutable contract for one HTTP handler. Ownership comes from verified authentication, never input fields. Production admission stays disabled until the HTTP execution adapter is qualified.
 */
export type OperationDefinitionSpec = {
  name: string;
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  path: string;
  owner: 'platform_tenant';
  /**
   * Bundled JSON Schema 2020-12; only local document references are supported.
   */
  input_schema: any;
  /**
   * Bundled schema for the business result selected by the execution target.
   */
  output_schema: any;
  progress_stages: Array<string>;
  completion_webhook_id?: string;
  recovery?: 'reconcile_on_unknown' | 'safe_retry';
};

