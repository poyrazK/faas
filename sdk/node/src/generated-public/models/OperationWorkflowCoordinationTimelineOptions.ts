/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowCoordinationTimelineOptions. Explicit business identity and live pagination selectors.
 */
export type OperationWorkflowCoordinationTimelineOptions = {
  /**
   * OperationWorkflowCoordinationTimelineOptions.app_id. Exact timeline query selector; customer identity comes from authentication.
   */
  app_id?: string;
  /**
   * OperationWorkflowCoordinationTimelineOptions.scope. Exact timeline query selector; customer identity comes from authentication.
   */
  scope?: string;
  /**
   * OperationWorkflowCoordinationTimelineOptions.tenant_id. Exact timeline query selector; customer identity comes from authentication.
   */
  tenant_id?: string;
  /**
   * OperationWorkflowCoordinationTimelineOptions.subject_type. Exact timeline query selector; customer identity comes from authentication.
   */
  subject_type?: string;
  /**
   * OperationWorkflowCoordinationTimelineOptions.subject_id. Exact timeline query selector; customer identity comes from authentication.
   */
  subject_id?: string;
  /**
   * OperationWorkflowCoordinationTimelineOptions.workflow. Exact timeline query selector; customer identity comes from authentication.
   */
  workflow?: string;
  /**
   * OperationWorkflowCoordinationTimelineOptions.instance_id. Exact timeline query selector; customer identity comes from authentication.
   */
  instance_id?: string;
  /**
   * OperationWorkflowCoordinationTimelineOptions.offset. Exact timeline query selector; customer identity comes from authentication.
   */
  offset?: number;
  /**
   * OperationWorkflowCoordinationTimelineOptions.limit. Exact timeline query selector; customer identity comes from authentication.
   */
  limit?: number;
};

