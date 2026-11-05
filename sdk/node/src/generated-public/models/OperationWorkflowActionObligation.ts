/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable source-and-kind identity derived from the persisted obligation epoch, including dependency-review revision where available. Ownership changes and deadline edits preserve the identity except a newly scheduled response check-in, which defines a new check-in obligation. Older saved actions may omit identity.
 */
export type OperationWorkflowActionObligation = {
  id: string;
  started_at: string;
};

