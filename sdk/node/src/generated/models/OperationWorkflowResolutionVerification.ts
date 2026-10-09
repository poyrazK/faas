/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlockerResolution } from './OperationWorkflowBlockerResolution.js';
export type OperationWorkflowResolutionVerification = {
  resolution: OperationWorkflowBlockerResolution;
  resolution_operation_id: string;
  resolution_report_id: string;
  resolution_revision: number;
  status: 'awaiting_verification' | 'verified';
  /**
   * Retained proof publication time.
   */
  verified_at?: string;
};

