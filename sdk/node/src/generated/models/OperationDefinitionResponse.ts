/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationDefinitionSpec } from './OperationDefinitionSpec.js';
/**
 * Definition identity, content revision and deployment pins.
 */
export type OperationDefinitionResponse = {
  id: string;
  app_id: string;
  scope: string;
  revision: string;
  deployment_id: string;
  release_id?: string;
  spec: OperationDefinitionSpec;
  created_at: string;
};

