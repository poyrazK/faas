/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedPostgresQueryError = {
  observed_at: string;
  severity: 'ERROR' | 'FATAL' | 'PANIC';
  sqlstate: string;
  /**
   * App binding when available.
   */
  binding_id?: string;
};

