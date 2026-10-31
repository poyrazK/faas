/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Canonical UUID request for a compute-class change on an existing managed PostgreSQL database.
 */
export type ResizeManagedPostgresDatabaseRequest = {
  /**
   * Canonical nonzero UUID; reuse with the same database and target after uncertain responses.
   */
  request_id: string;
  service_class: 'development' | 'burstable' | 'production';
};

