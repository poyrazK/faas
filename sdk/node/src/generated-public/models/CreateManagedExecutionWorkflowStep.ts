/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateExecutionRequest } from './CreateExecutionRequest.js';
import type { ManagedExecutionWorkflowArtifactInput } from './ManagedExecutionWorkflowArtifactInput.js';
/**
 * One disposable Run in the DAG and its dependency, result, and artifact handoff rules.
 */
export type CreateManagedExecutionWorkflowStep = {
  label: string;
  /**
   * Earlier step labels that must succeed before this step is admitted.
   */
  depends_on?: Array<string>;
  /**
   * Use the immediately preceding successful Run's JSON result as this step's input; adds that step as a dependency.
   */
  input_from_previous_result?: boolean;
  /**
   * Set input to a JSON object keyed by dependency label, with each dependency's terminal status and successful JSON result when available.
   */
  include_dependency_results?: boolean;
  /**
   * Artifacts from earlier successful steps to stage in this Run's fresh ephemeral files bundle; each producer becomes an implicit dependency.
   */
  artifact_inputs?: Array<ManagedExecutionWorkflowArtifactInput>;
  /**
   * Optional JSON Schema Draft 2020-12 contract for this Run's JSON result. The control plane enforces it before dependents can consume the result. Local fragment references are supported; external resources are not loaded. Each schema is limited to 64 KiB and all schemas in a workflow together are limited to 1 MiB.
   */
  result_schema?: (Record<string, any> | boolean);
  /**
   * Must not include workflow_id, step_label, or artifact_inputs.
   */
  request: CreateExecutionRequest;
};

