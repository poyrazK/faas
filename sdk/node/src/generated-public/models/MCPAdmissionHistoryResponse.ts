/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPAdmissionEvidence } from './MCPAdmissionEvidence.js';
/**
 * Account-owned admission receipts ordered by descending audit sequence with a continuation cursor.
 */
export type MCPAdmissionHistoryResponse = {
  items: Array<MCPAdmissionEvidence>;
  limit: number;
  next_cursor?: string;
};

