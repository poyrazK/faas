/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EgressFlowLogEntry } from './EgressFlowLogEntry.js';
/**
 * Egress flow log rows, newest first. truncated means the page limit was reached.
 */
export type EgressFlowLogResponse = {
  flows: Array<EgressFlowLogEntry>;
  truncated: boolean;
};

