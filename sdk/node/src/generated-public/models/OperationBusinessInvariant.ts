/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application-evaluated business condition. String limits are UTF-8 bytes; instance, version, and description must be nonempty without control characters.
 */
export type OperationBusinessInvariant = {
  workflow: string;
  instance_id: string;
  state: string;
  code: string;
  version: string;
  status: 'passed' | 'failed' | 'unknown';
  description: string;
  operations: Array<string>;
};

