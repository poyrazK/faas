/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresAccountingDiagnostic } from './ManagedPostgresAccountingDiagnostic.js';
/**
 * One live account-scoped page of local accounting evidence.
 */
export type ManagedPostgresAccountingDiagnosticsResponse = {
  account_id: string;
  evaluated_at: string;
  policy_enabled: boolean;
  window_seconds: number;
  items: Array<ManagedPostgresAccountingDiagnostic>;
  next_cursor?: string | null;
};

