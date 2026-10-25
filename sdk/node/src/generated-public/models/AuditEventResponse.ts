/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the customer's security event timeline. The `kind` taxonomy and per-kind `data` schema are documented in `docs/adr/035-auth-audit-events.md`.
 */
export type AuditEventResponse = {
  /**
   * Audit event row id (bigint as string).
   */
  id: string;
  /**
   * When the event was recorded (RFC 3339, UTC).
   */
  at: string;
  /**
   * Which daemon wrote the row. `apid` for IAM-4 surface; `schedd` for state-transition events (instance wakes / parks / watchdog timeouts).
   */
  actor: string;
  /**
   * Namespaced event kind. Common values: `auth.login`, `auth.logout`, `auth.session.created`, `auth.session.revoke`, `auth.sessions.revoke_all`, `auth.session.stolen`, `key.created`, `key.deleted`, `secret.set`, `secret.deleted`, `env.set`, `env.deleted`, `account.plan_changed`, `account.deletion_scheduled`, `account.deletion_restored`.
   */
  kind: string;
  /**
   * Account id (uuid string form) the event was recorded against. Omitted when the event has no subject (e.g. system-level).
   */
  subject?: string;
  /**
   * Highest-severity classification for stateless.advisory rows (`high` for /data, /db, /var/lib/postgresql, /var/lib/mysql; `warn` for other closed paths; `info` for an empty batch). Omitted for non-stateless kinds and for pre-PR-427 stateless.advisory rows where the data.severity key is absent. Mega-PR B / stateless-DX work.
   */
  severity?: 'high' | 'warn' | 'info';
  /**
   * Kind-specific payload. Always a JSON object; the inner shape depends on `kind`. Plaintext values (e.g. secret VALUE) are NEVER carried in `data`.
   */
  data: Record<string, any>;
};

