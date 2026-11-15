/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only computed relationship joining selected source columns to the target complete primary key. Both rows remain subject to RLS and exact tenant equality; forged RPC row attributes do not grant access.
 */
export type DataPipelineRelationship = {
  name: string;
  source_table: string;
  target_table: string;
  source_columns: Array<string>;
  target_columns: Array<string>;
};

