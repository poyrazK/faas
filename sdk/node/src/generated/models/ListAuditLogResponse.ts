/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AuditLogEntry } from './AuditLogEntry.js';
/**
 * Envelope for `GET /v1/audit-log` and `GET /v1/audit-log/all`
 * (issue #755 / PR-6). `entries` is newest-first
 * (`received_at DESC, id DESC`) so the dashboard can render
 * top-of-list without re-sorting. `limit` echoes the
 * effective limit applied by the handler so the SDK can
 * render `showing N of M` without re-issuing the request.
 *
 */
export type ListAuditLogResponse = {
  entries: Array<AuditLogEntry>;
  /**
   * Effective page size applied (always 1..100; mirrors the customer and operator /v1/audit-log routes).
   */
  limit: number;
};

