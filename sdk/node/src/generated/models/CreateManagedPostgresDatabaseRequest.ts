/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Customer request to reserve a managed PostgreSQL database.
 */
export type CreateManagedPostgresDatabaseRequest = {
  name: string;
  region: string;
  postgres_major?: number;
  service_class?: 'development' | 'burstable' | 'production';
  availability?: 'single_zone' | 'high_availability';
  scale_to_zero?: boolean;
  storage_limit_bytes?: number;
  restore_window_seconds?: number;
};

