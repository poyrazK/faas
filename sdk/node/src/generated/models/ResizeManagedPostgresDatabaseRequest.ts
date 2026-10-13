/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Canonical UUID request for a compute-class change, a storage-limit increase, or both, on an existing managed PostgreSQL database. At least one of service_class and storage_limit_bytes is required.
 */
export type ResizeManagedPostgresDatabaseRequest = {
  /**
   * Canonical nonzero UUID; reuse with the same database and target after uncertain responses.
   */
  request_id: string;
  /**
   * Target class; omitted keeps the class.
   */
  service_class?: 'development' | 'burstable' | 'production';
  /**
   * Target storage limit; omitted, zero or the current limit keeps it. Must not be lower than the current limit.
   */
  storage_limit_bytes?: number;
};

