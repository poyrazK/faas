/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One workload the branch published from its parent's build. It starts on its first request.
 */
export type ProjectEnvironmentBranchWorkload = {
  workload: string;
  deployment_id: string;
  /**
   * The parent deployment whose build the branch reuses.
   */
  source_deployment_id: string;
  status: string;
  /**
   * The workload's address in the branch. Its first request starts it.
   */
  url?: string;
};

