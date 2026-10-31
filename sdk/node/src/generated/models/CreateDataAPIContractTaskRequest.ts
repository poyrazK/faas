/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The contract inspection to run.
 */
export type CreateDataAPIContractTaskRequest = {
  /**
   * `types` (TypeScript contract), `validators` (input validators) or `schema` (JSON contract snapshot).
   */
  kind: 'types' | 'validators' | 'schema';
};

