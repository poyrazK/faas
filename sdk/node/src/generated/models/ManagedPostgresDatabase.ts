/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Managed PostgreSQL metadata. Provider IDs and credentials are never returned.
 */
export type ManagedPostgresDatabase = {
  id: string;
  name: string;
  region: string;
  postgres_major: number;
  service_class: 'development' | 'burstable' | 'production';
  availability: 'single_zone' | 'high_availability';
  scale_to_zero: boolean;
  storage_limit_bytes: number;
  restore_window_seconds: number;
  restore_source_database_id?: string | null;
  restore_point_in_time?: string | null;
  state: 'provisioning' | 'ready' | 'updating' | 'deleting' | 'failed' | 'deleted';
  last_error_code?: string | null;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
};

