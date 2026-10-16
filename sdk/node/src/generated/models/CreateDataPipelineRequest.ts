/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selected public tables, dedicated destination and explicitly authorized JWT readers.
 */
export type CreateDataPipelineRequest = {
  name: string;
  source_connection_id: string;
  database_id: string;
  tables: Array<string>;
  read_subjects: Array<string>;
};

