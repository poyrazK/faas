/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable fenced capacity inventory and quota rebase. Failed or blocked jobs retain reservations; billing is unaffected.
 */
export type ObjectCapacityReconciliation = {
  id: string;
  bucket_id: string;
  state: 'waiting' | 'scanning' | 'completed' | 'blocked' | 'failed' | 'cancelled';
  before_bytes: number;
  before_keys: number;
  after_bytes: number;
  after_keys: number;
  reclaimed_bytes: number;
  reclaimed_keys: number;
  pending_writes: number;
  last_error_code?: string;
  created_at: string;
  updated_at: string;
  finished_at?: string;
};

