/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A fixed public HTTPS destination, maximum HTTP route policy, and optional daily admitted-request limit.
 */
export type CreateOutboundIntegrationRequest = {
  name: string;
  origin: string;
  allowed_methods: Array<'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'>;
  allowed_path_prefixes: Array<string>;
  /**
   * Optional per-integration daily admitted-request limit; account plan ceilings may be lower.
   */
  daily_request_limit?: number | null;
};

