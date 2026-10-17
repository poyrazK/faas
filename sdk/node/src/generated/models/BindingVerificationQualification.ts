/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationQualificationCondition } from './BindingVerificationQualificationCondition.js';
/**
 * Server-confirmed temporary qualification conditions for the original receipt. No admission or release authority; wait within its original expiry and recheck.
 */
export type BindingVerificationQualification = {
  checked_at: string;
  conditions: Array<BindingVerificationQualificationCondition>;
};

