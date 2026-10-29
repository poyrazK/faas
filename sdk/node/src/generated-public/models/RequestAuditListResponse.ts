/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAuditRecord } from './RequestAuditRecord.js';
/**
 * Bounded exact gateway-request audit window, newest first.
 */
export type RequestAuditListResponse = {
  app_id: string;
  records: Array<RequestAuditRecord>;
  since: string;
  until: string;
};

