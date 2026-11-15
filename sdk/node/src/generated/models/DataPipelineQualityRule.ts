/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Structured business-data assertion on selected columns. Numeric bounds are exact decimal strings; reference rules match a complete selected target primary key and preserve tenant equality. SQL expressions are not accepted.
 */
export type DataPipelineQualityRule = {
  name: string;
  kind: 'required' | 'unique' | 'range' | 'reference';
  table: string;
  columns: Array<string>;
  minimum?: string;
  maximum?: string;
  reference_table?: string;
  reference_columns?: Array<string>;
};

