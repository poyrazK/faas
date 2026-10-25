/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AuditEventResponse } from './AuditEventResponse.js';
/**
 * Envelope for `GET /v1/audit-events`. `limit` echoes the effective limit applied by the handler so the SDK can render `showing N of M` without re-issuing the request.
 */
export type ListAuditEventsResponse = {
  events: Array<AuditEventResponse>;
  /**
   * Effective limit applied (always 1..100).
   */
  limit: number;
};

