/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingRunDraftExportRow } from './BillingRunDraftExportRow.js';
/**
 * One page of a run's review and the current draft-only connection, when available.
 */
export type BillingRunDraftExportReview = {
  items: Array<BillingRunDraftExportRow>;
  next_cursor?: string;
  livemode?: boolean;
  connection_updated_at?: string;
  connection_problem?: string;
};

