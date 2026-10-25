/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookStepDependency. One step waits for all named prerequisite reviews in the same pinned outcome checklist; unknown labels, self-edges, duplicates and cycles are rejected.
 */
export type OperationWorkflowPlaybookStepDependency = {
  /**
   * OperationWorkflowPlaybookStepDependency.label. Exact declared dependent step label, unique within the graph.
   */
  label: string;
  /**
   * OperationWorkflowPlaybookStepDependency.prerequisites. Exact declared prerequisites; each must be reviewed complete with its own prerequisites satisfied.
   */
  prerequisites: Array<string>;
};

