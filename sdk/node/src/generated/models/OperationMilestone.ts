/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowStep } from './OperationWorkflowStep.js';
/**
 * Retained public business fact. Deduplicated by Operation and milestone ID across execution generations, retained with the Operation, and attributed to its immutable business reference.
 */
export type OperationMilestone = {
  /**
   * App-declared workflow labels matched to this retained milestone; only observed steps are included.
   */
  workflow_steps?: Array<OperationWorkflowStep>;
  id: string;
  operation_id: string;
  /**
   * Only account operator feeds include this customer identity.
   */
  platform_tenant_id?: string;
  subject?: OperationSubject;
  name: string;
  /**
   * Retained, schema-validated public JSON fact. Read through the existing customer or account ownership boundary.
   */
  payload: any;
  occurred_at: string;
  /**
   * First platform publication time; determines descending timeline order.
   */
  created_at: string;
  /**
   * Corresponding Operation event sequence. The general event stream contains a notice; this ledger retains the payload.
   */
  sequence: number;
};

