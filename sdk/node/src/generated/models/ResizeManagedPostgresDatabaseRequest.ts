/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ResizeManagedPostgresDatabaseRequest = {
  /**
   * Canonical nonzero UUID; reuse with the same database and target after uncertain responses.
   */
  request_id: string;
  service_class: 'development' | 'burstable' | 'production';
};

