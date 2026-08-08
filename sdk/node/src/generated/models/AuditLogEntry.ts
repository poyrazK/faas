/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the FK-free `audit_log` table (issue #755 /
 * PR-6). The row outlives the account it relates to so a
 * regulator / DPO can re-derive the post-deletion state
 * without joining back to a deleted `accounts` row. The
 * table is append-only by spec (ISO 27001 SoA A.5.33 —
 * retention forever); there is no UPDATE / DELETE
 * permission in production and no Go-side path that would
 * emit one.
 *
 */
export type AuditLogEntry = {
  /**
   * Audit-log row id (uuid canonical form).
   */
  id: string;
  /**
   * Namespaced audit-log kind. The PR-6 surface currently
   * emits only `account.deleted` from inside
   * `(*PgStore).DeleteAccount` / `(*MemStore).DeleteAccount`
   * so the regulator can replay post-deletion state. The
   * closed vocabulary will widen in a follow-up PR if a
   * future audit emitter reuses the table for a new
   * evidence surface.
   *
   */
  kind: string;
  /**
   * Account id (uuid canonical form) the row was recorded
   * against. Nullable in the schema (anonymous /
   * background activity can emit rows); omitted on the
   * wire when the column is NULL.
   *
   */
  account_id?: string;
  /**
   * Email captured at copy-time inside
   * `DeleteAccount`. The audit row is self-contained: a
   * regulator reading a row for a UUID that no longer
   * exists in `accounts` still sees the human identifier
   * without joining back to a deleted accounts row.
   * Omitted when the row was emitted by anonymous
   * activity.
   *
   */
  account_email?: string;
  /**
   * Which daemon wrote the row. PR-6 emits `grace-sweep`
   * from inside the DeleteAccount store method. Future
   * emitters (follow-up PRs) will widen the vocabulary.
   *
   */
  actor?: string;
  /**
   * When the audit-log row was recorded (RFC 3339, UTC).
   */
  received_at: string;
  /**
   * Kind-specific payload. Always a JSON object; the
   * inner shape depends on `kind`. For
   * `account.deleted`, the payload carries `source`
   * (`grace-sweep` today), `email` (the verbatim
   * captured email), and `actor` (`grace-sweep` today)
   * so the dashboard can render the row without joining
   * back to a deleted `accounts` row.
   *
   */
  data?: Record<string, any>;
};

