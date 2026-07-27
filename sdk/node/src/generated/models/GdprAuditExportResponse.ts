/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the customer's audit trail in the GDPR export bundle. Two row kinds live here: source=`gdpr` carries a self-service action (`export`, `delete`, `restore`); source=`event` carries a security event from the events table (IAM-4, ADR-035) with a `kind` and JSON `data` payload. The two are interleaved by timestamp descending.
 */
export type GdprAuditExportResponse = {
  source: 'gdpr' | 'event';
  action?: 'export' | 'delete' | 'restore';
  requested_at: string;
  completed_at?: string | null;
  /**
   * Security event kind. Populated only when `source` = `event`. Examples: `auth.login`, `key.created`, `secret.set`, `account.deletion_scheduled`.
   */
  kind?: string;
  /**
   * Kind-specific JSON payload from the events row. Populated only when `source` = `event`. Plaintext values (e.g. secret VALUE) are NEVER carried in `data`.
   */
  data?: Record<string, any>;
};

