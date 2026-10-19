/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An immutable logging endpoint with credential presence and a configuration hash.
 */
export type ApplicationStandardLogDestination = {
  id: string;
  org_id: string;
  name: string;
  kind: 'http_json' | 'otlp';
  target_url: string;
  has_auth_header: boolean;
  config_hash: string;
  created_by: string;
  created_at: string;
};

