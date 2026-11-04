/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One durable in-app reminder marker for the original notification and follow-up deadline; rescheduling or clearing retires it. Does not authorize execution.
 */
export type OperationWorkflowReadinessDueReminder = {
  /**
   * OperationWorkflowReadinessDueReminder.id. Stable UUID derived from notification identity and normalized deadline, shared across retries and restarts.
   */
  id: string;
  /**
   * OperationWorkflowReadinessDueReminder.due_at. Deadline this reminder belongs to.
   */
  due_at: string;
  /**
   * OperationWorkflowReadinessDueReminder.created_at. Successful reminder publication time.
   */
  created_at: string;
};

