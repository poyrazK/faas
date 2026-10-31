/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAITrace } from './OutboundAITrace.js';
/**
 * One page of AI traces, newest last call first.
 */
export type OutboundAITraceList = {
  items: Array<OutboundAITrace>;
  /**
   * The last page item's last call, to pass as before for older traces; null on the last page.
   */
  next_before: string | null;
};

