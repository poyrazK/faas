/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SelectedTraceApp } from './SelectedTraceApp.js';
import type { SelectedTraceInvocation } from './SelectedTraceInvocation.js';
import type { SelectedTraceRequest } from './SelectedTraceRequest.js';
/**
 * Independent selected-app source reads, not an atomic snapshot or a complete trace. Empty retained results do not establish health. Both unavailable sources still return an explicit partial response.
 */
export type SelectedTraceLookupResponse = {
  selection: 'explicit_app_ids_v1';
  trace_id: string;
  apps: Array<SelectedTraceApp>;
  generated_at: string;
  limit: number;
  request_window_start: string;
  request_window_end: string;
  request_retention_days: number;
  requests: Array<SelectedTraceRequest>;
  requests_status: 'ok' | 'unavailable';
  requests_truncated: boolean;
  invocations: Array<SelectedTraceInvocation>;
  invocations_status: 'ok' | 'unavailable';
  invocations_truncated: boolean;
  partial: boolean;
};

