/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Select a declared output from an earlier workflow step and stage it at a relative path in this step's ephemeral bundle.
 */
export type ManagedExecutionWorkflowArtifactInput = {
  from_step: string;
  /**
   * Normalized output_files name declared by the producer step.
   */
  name: string;
  /**
   * Normalized relative destination path in the consumer Run's ephemeral files bundle.
   */
  path: string;
};

