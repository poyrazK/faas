/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Runtime log destination with sealed credentials represented by a mask.
 */
export type AppLogDrainResponse = {
  id: string;
  app_id: string;
  account_id: string;
  kind: 'http_json' | 'otlp';
  target_url: string;
  auth_header_masked: '' | '***';
  enabled: boolean;
  created_at: string;
  updated_at: string;
};
