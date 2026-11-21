/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Scoped Agent endpoint, thread, call, task version and input path for staging a file.
 */
export type CreateAgentExecutionFileGrantRequest = {
  endpoint_id: string;
  thread_id: string;
  tool_call_id: string;
  task_name: string;
  version: number;
  /**
   * Exact pipeline step label. Omit for a standalone Agent task call.
   */
  step_label?: string;
  /**
   * Normalized relative path in the guest input directory.
   */
  path: string;
};

