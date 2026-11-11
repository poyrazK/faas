/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowLiveVerification } from './StoredWorkflowLiveVerification.js';
/**
 * Current target identities and the twenty most recent retained live verifications.
 */
export type ListStoredWorkflowLiveVerificationsResponse = {
  deployment_id?: string;
  published_version: number;
  verifications: Array<StoredWorkflowLiveVerification>;
};

