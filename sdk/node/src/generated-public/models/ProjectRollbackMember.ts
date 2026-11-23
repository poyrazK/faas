/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A service revision recorded in a production project deploy.
 */
export type ProjectRollbackMember = {
  /**
   * App slug when the app is still attached to the project.
   */
  slug?: string;
  deployment_id: string;
  revision: number;
  scope: string;
  status: string;
  /**
   * Whether the revision has a recorded end to a past serving period.
   */
  previously_served: boolean;
  predecessor_id?: string;
  predecessor_revision?: number;
};

