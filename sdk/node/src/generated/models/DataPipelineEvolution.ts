/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineServing } from './DataPipelineServing.js';
/**
 * Frozen account and generation-bound serving preview. Approved indicates owner intent; applied requires worker verification and any required API cache refresh.
 */
export type DataPipelineEvolution = {
  readonly retry?: number;
  id: string;
  state: 'preview' | 'approved' | 'applied' | 'superseded';
  kind: 'projection' | 'auth_organizations' | 'relationships';
  generation: number;
  preview_revision: number;
  before?: DataPipelineServing;
  after: DataPipelineServing;
  added_tables?: Array<string>;
};

