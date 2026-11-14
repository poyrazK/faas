/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One managed PostgreSQL copy a branch owns.
 */
export type ProjectEnvironmentBranchDatabase = {
  source_database_id: string;
  name: string;
  /**
   * Empty until the copy is made.
   */
  copy_database_id?: string;
};

