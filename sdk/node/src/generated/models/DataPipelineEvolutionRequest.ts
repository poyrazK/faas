/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineAuthOrganizations } from './DataPipelineAuthOrganizations.js';
import type { DataPipelineRelationship } from './DataPipelineRelationship.js';
/**
 * Revision-fenced owner evolution action. Preview supplies one kind and its complete selection or mapping. Approve and discard supply only a saved change_id; approval never accepts replacement intent.
 */
export type DataPipelineEvolutionRequest = {
  expected_revision: number;
  action: 'preview' | 'approve' | 'discard' | 'retry';
  change_id?: string;
  kind?: 'projection' | 'auth_organizations' | 'relationships';
  columns?: Record<string, Array<string>>;
  auth_organizations?: DataPipelineAuthOrganizations;
  relationships?: Array<DataPipelineRelationship>;
};

