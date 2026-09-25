/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentDomainResponse } from './ProjectEnvironmentDomainResponse.js';
/**
 * A hostname added to, removed from, or changed between two environments.
 */
export type ProjectEnvironmentDomainChangeResponse = {
  domain: string;
  kind: 'added' | 'removed' | 'changed';
  before?: ProjectEnvironmentDomainResponse;
  after?: ProjectEnvironmentDomainResponse;
};

