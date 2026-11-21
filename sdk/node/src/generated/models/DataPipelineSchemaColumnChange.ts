/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One exact source column or table change and the worker's compatibility decision.
 */
export type DataPipelineSchemaColumnChange = {
  table: string;
  column: string;
  kind: string;
  before_type?: string;
  after_type?: string;
  reason: string;
  safe: boolean;
};

