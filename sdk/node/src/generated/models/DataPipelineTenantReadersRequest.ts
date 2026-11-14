/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineTenantMembership } from './DataPipelineTenantMembership.js';
/**
 * Account-owner-only, revision-fenced membership preview, approval or discard. Preview supplies the complete replacement membership array. Approve supplies only the saved change_id; grace permits strict membership reductions only.
 */
export type DataPipelineTenantReadersRequest = {
  expected_revision: number;
  action: 'preview' | 'approve' | 'discard';
  change_id?: string;
  memberships?: Array<DataPipelineTenantMembership>;
};

