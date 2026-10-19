/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical release selection used to prepare a checked project rollback.
 */
export type PrepareProjectRollbackRequest = {
  /**
   * Retained inactive historical graph in this project environment.
   */
  release_id: string;
  ttl_seconds: number;
};

