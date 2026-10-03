/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentBindingResponse } from './ProjectEnvironmentBindingResponse.js';
/**
 * Managed resource binding change between two environment states.
 */
export type ProjectEnvironmentBindingChangeResponse = {
  kind: 'managed_postgres' | 'object_storage';
  binding_id: string;
  change: 'added' | 'removed' | 'changed';
  before?: ProjectEnvironmentBindingResponse;
  after?: ProjectEnvironmentBindingResponse;
};

