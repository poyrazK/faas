/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One recorded config change, without values (ADR-1451).
 */
export type ConfigRevision = {
  revision: number;
  scope: string;
  source: 'baseline' | 'env.set' | 'env.delete' | 'secret.set' | 'secret.delete' | 'secret.rotate' | 'config.rollback' | 'config.guard_restore';
  key?: string;
  actor: string;
  created_at: string;
  variable_count: number;
  secret_count: number;
  guard_id?: string;
  guard_status?: string;
  restored_revision?: number;
};

