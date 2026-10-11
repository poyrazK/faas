/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessCompletion } from './OperationWorkflowReadinessCompletion.js';
/**
 * OperationWorkflowReadinessCoordination. Shared inbox coordination with optimistic revision checks; omission of a follow-up time clears it.
 */
export type OperationWorkflowReadinessCoordination = {
  completion?: OperationWorkflowReadinessCompletion;
  /**
   * OperationWorkflowReadinessCoordination.revision. Shared coordination value; does not alter application evidence.
   */
  revision: number;
  /**
   * OperationWorkflowReadinessCoordination.status. Shared coordination value; does not alter application evidence.
   */
  status: 'unread' | 'read' | 'acknowledged';
  /**
   * OperationWorkflowReadinessCoordination.owner. Shared coordination value; does not alter application evidence.
   */
  owner: string;
  /**
   * OperationWorkflowReadinessCoordination.note. Shared coordination value; does not alter application evidence.
   */
  note: string;
  /**
   * OperationWorkflowReadinessCoordination.follow_up_at. Shared coordination value; does not alter application evidence.
   */
  follow_up_at?: string;
  /**
   * OperationWorkflowReadinessCoordination.updated_at. Shared coordination value; does not alter application evidence.
   */
  updated_at: string;
  /**
   * OperationWorkflowReadinessCoordination.acknowledged_at. Shared coordination value; does not alter application evidence.
   */
  acknowledged_at?: string;
  /**
   * OperationWorkflowReadinessCoordination.updated_by. Shared coordination value; does not alter application evidence.
   */
  updated_by: string;
  /**
   * OperationWorkflowReadinessCoordination.acknowledged_by. Shared coordination value; does not alter application evidence.
   */
  acknowledged_by?: string;
};

