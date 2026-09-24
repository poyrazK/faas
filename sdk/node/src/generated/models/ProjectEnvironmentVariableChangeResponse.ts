/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-secret runtime variable change between two environments.
 */
export type ProjectEnvironmentVariableChangeResponse = {
  key: string;
  kind: 'added' | 'removed' | 'changed';
  before?: string;
  after?: string;
};

