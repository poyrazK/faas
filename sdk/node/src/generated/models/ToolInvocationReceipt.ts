/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolInvocationRecord } from './ToolInvocationRecord.js';
/**
 * Invocation transition receipt; creation and dispatch flags are fresh transition facts and are false on duplicates.
 */
export type ToolInvocationReceipt = {
  created: boolean;
  dispatch_granted: boolean;
  record: ToolInvocationRecord;
};

