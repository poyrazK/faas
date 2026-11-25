/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type SetManagedPostgresBackupScheduleRequest = {
  daily_at_utc: string;
  /**
   * App slug that owns the destination bucket.
   */
  bucket_app: string;
  bucket_id: string;
};

