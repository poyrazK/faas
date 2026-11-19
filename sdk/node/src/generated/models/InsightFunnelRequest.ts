/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightStepRequest } from './InsightStepRequest.js';
/**
 * A funnel query.
 */
export type InsightFunnelRequest = {
  steps: Array<InsightStepRequest>;
  from?: string;
  until?: string;
  /**
   * Go duration from the first step to the last, 1h to 720h; default 168h.
   */
  window?: string;
  unit?: 'customer' | 'subject';
  /**
   * flag:KEY or property:KEY.
   */
  by?: string;
};

