/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Whether a scale-to-zero database starts when an app bound to it wakes.
 */
export type SetManagedPostgresWakeWithAppRequest = {
  /**
   * True starts the database with its app; false leaves it to start on the app's first query.
   */
  enabled: boolean;
};

