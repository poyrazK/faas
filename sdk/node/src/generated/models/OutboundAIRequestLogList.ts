/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIRequestLogEntry } from './OutboundAIRequestLogEntry.js';
export type OutboundAIRequestLogList = {
  items: Array<OutboundAIRequestLogEntry>;
  /**
   * Pass as before to read the next page; null on the last page.
   */
  next_before: string | null;
};

