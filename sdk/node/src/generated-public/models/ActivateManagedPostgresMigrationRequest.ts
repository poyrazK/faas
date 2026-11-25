/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ActivateManagedPostgresMigrationRequest = {
  source_database_id: string;
  target_database_id: string;
  operation_id: string;
  /**
   * UUID of the current write pause and stable switch ID.
   */
  verification_id: string;
  /**
   * The operator confirms source writers are paused and drained. Must be true.
   */
  source_writes_paused: boolean;
  scope?: string;
};

