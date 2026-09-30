/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagEvidence } from './FlagEvidence.js';
/**
 * Retained request aggregate containing bounded flag decisions.
 */
export type FlagRequestEvidence = {
  id: string;
  app_id: string;
  deployment_id: string;
  customer_id?: string;
  received_at: string;
  route: string;
  method: string;
  status: number;
  latency_ms: number;
  count: number;
  cold_boot: boolean;
  trace_id?: string;
  flags: Array<FlagEvidence>;
};

