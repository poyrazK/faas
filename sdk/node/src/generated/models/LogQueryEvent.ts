/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe source-neutral log query event; trace lookup returns the HTTP access-log projection.
 */
export type LogQueryEvent = {
  id: string;
  app?: string;
  timestamp: string;
  source: 'runtime' | 'http';
  deployment_id?: string;
  instance_id?: string;
  request_id?: string;
  trace_id?: string;
  route?: string;
  method?: string;
  status?: number;
  level?: 'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal';
  stream?: 'stdout' | 'stderr' | 'system' | 'access';
  message: string;
  latency_ms?: number;
  count?: number;
  cold_boot?: boolean;
};

