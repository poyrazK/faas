/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selected public tables, dedicated destination and explicitly authorized JWT readers. A fresh passing connection check for the current source credential and all selected tables is required; otherwise returns data_connection_check_required.
 */
export type CreateDataPipelineRequest = {
  name: string;
  source_connection_id: string;
  database_id: string;
  tables: Array<string>;
  read_subjects: Array<string>;
};

