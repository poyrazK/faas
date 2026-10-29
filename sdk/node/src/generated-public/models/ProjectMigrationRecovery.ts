/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable operator resolution and verified roll-forward identity. The cleanup outcome remains unchanged; database and execution checks are operator attestations.
 */
export type ProjectMigrationRecovery = {
  status: 'blocked' | 'needs_operator' | 'not_required' | 'resolved';
  reason?: string;
  request_id?: string;
  task_id?: string;
  expected_generation?: number;
  replacement_attempt_id?: string;
  replacement_release_id?: string;
  evidence_hash?: string;
  operator_account_id?: string;
  resolved_at?: string;
};

