/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedPostgresBackupSchedule = {
  database_id: string;
  bucket_app_id: string;
  bucket_id: string;
  daily_at_utc: string;
  next_run_at: string;
  state: 'scheduled' | 'pending' | 'running';
  active_run_id?: string;
  last_success_at?: string;
  /**
   * Object key of the most recent complete backup.
   */
  last_archive_key?: string;
  /**
   * Stable code for the latest backup attempt failure.
   */
  last_error_code?: string;
};

