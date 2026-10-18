/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The moment before the earliest crossed release for one managed PostgreSQL database the app's release commands can migrate (ADR-1448), with the command that restores it into a new database.
 */
export type ReleaseRestorePoint = {
  database_id: string;
  database_name: string;
  point_in_time: string;
  command: string;
};

