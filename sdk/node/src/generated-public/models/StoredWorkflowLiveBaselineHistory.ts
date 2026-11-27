/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowLiveBaseline } from './StoredWorkflowLiveBaseline.js';
import type { StoredWorkflowLiveBaselineRetirement } from './StoredWorkflowLiveBaselineRetirement.js';
/**
 * Bounded promotion audit history and optional permanent retirement receipt for one baseline name.
 */
export type StoredWorkflowLiveBaselineHistory = {
  retirement?: StoredWorkflowLiveBaselineRetirement;
  promotions: Array<StoredWorkflowLiveBaseline>;
};

