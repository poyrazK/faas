/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineTenantMembership } from './DataPipelineTenantMembership.js';
/**
 * Frozen membership preview. Approval applies this exact after set atomically at the mirror before catalog acknowledgement.
 */
export type DataPipelineTenantChange = {
  id: string;
  state: 'preview' | 'approved' | 'applied';
  preview_revision: number;
  before: Array<DataPipelineTenantMembership>;
  after: Array<DataPipelineTenantMembership>;
};

