/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveVerification } from './AutomationLiveVerification.js';
/**
 * Current target identities and the twenty most recent retained live verifications.
 */
export type ListAutomationLiveVerificationsResponse = {
  deployment_id?: string;
  published_version: number;
  verifications: Array<AutomationLiveVerification>;
};

