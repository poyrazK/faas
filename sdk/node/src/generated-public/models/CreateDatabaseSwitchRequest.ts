/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Restore a managed database the app binds and move the app to the copy, keeping its code.
 */
export type CreateDatabaseSwitchRequest = {
  /**
   * The managed database the app's bindings point at.
   */
  database_id: string;
  /**
   * Name of the restored copy; it becomes the app's database.
   */
  name: string;
  /**
   * A time inside the database's restore range. Writes after it stay only in the source database.
   */
  point_in_time: string;
  /**
   * The environment whose bindings move; empty is the default scope.
   */
  scope?: string;
};

