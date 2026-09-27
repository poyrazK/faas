/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-authorized handle for one callback wait step.
 */
export type WorkflowCallbackResponse = {
  id: string;
  step_name: string;
  /**
   * Present after the wait activates.
   */
  expires_at?: string;
};

