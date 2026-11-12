/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One key-level change between two environment configuration snapshots.
 */
export type ProjectEnvironmentConfigChange = {
  key: string;
  kind: 'added' | 'removed' | 'changed';
  before?: any;
  after?: any;
};

