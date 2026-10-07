/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationMilestoneRequest } from './OperationMilestoneRequest.js';
/**
 * Bounded read-only precommit validation; does not reserve capacity, extend the claim, or publish facts. Maximum request size 65536 bytes.
 */
export type OperationMilestoneValidationRequest = {
  milestones: Array<OperationMilestoneRequest>;
};

