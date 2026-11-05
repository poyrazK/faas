/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAITrace } from './OutboundAITrace.js';
import type { OutboundAITraceCall } from './OutboundAITraceCall.js';
/**
 * One AI trace with its logged calls, oldest first.
 */
export type OutboundAITraceResponse = {
  trace: OutboundAITrace;
  calls: Array<OutboundAITraceCall>;
  /**
   * Whether the trace logged more calls than were returned.
   */
  calls_truncated: boolean;
};

