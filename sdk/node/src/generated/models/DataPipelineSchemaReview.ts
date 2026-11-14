/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineSchemaColumnChange } from './DataPipelineSchemaColumnChange.js';
/**
 * Worker-generated exact source-definition review with column and table metadata only. Its ID binds the generation, recorded baseline and requested source schema.
 */
export type DataPipelineSchemaReview = {
  id: string;
  state: string;
  supported: boolean;
  changes: Array<DataPipelineSchemaColumnChange>;
  requested_tables?: Array<string>;
};

