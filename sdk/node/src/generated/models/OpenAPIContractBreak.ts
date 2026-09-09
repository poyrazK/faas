/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One structural breaking change in an API response schema.
 */
export type OpenAPIContractBreak = {
  path: string;
  method: 'get' | 'put' | 'post' | 'delete' | 'options' | 'head' | 'patch' | 'trace';
  status?: string;
  kind: 'type_change' | 'field_removed' | 'required_added' | 'nullability_change';
  path_in_schema?: string;
  before?: any;
  after?: any;
};

