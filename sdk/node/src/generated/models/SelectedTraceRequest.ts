/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained metadata only; no spans, headers, payloads or infrastructure fields.
 */
export type SelectedTraceRequest = {
  app_id: string;
  id: string;
  deployment_id?: string;
  route: string;
  method: string;
  status: number;
  latency_ms: number;
  count: number;
  cold_boot: boolean;
  received_at: string;
};

