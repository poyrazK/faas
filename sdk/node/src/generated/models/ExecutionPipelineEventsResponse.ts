/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineEvent } from './ExecutionPipelineEvent.js';
import type { ExecutionPipelineProgress } from './ExecutionPipelineProgress.js';
/**
 * Resumable page of pipeline journal entries with the current state and gap information.
 */
export type ExecutionPipelineEventsResponse = {
  pipeline_id: string;
  events: Array<ExecutionPipelineEvent>;
  next_sequence: number;
  gap?: boolean;
  terminal: boolean;
  progress?: ExecutionPipelineProgress;
};

