/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentSecretCellResponse } from './ProjectEnvironmentSecretCellResponse.js';
export type ProjectEnvironmentSecretChangeResponse = {
  key: string;
  kind: 'added' | 'removed' | 'changed' | 'unknown';
  before: ProjectEnvironmentSecretCellResponse;
  after: ProjectEnvironmentSecretCellResponse;
};

