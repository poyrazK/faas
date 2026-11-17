/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Deploys one workload of a simulated environment branch into a rehearsal member's developer session, reusing the branch's build.
 */
export type SeedDevSessionFromEnvironmentBranchRequest = {
  /**
   * The rehearsal run's workspace ID.
   */
  workspace_id: string;
  /**
   * Slug of the project that owns the branch.
   */
  project: string;
  /**
   * Slug of the simulated branch environment.
   */
  environment: string;
  /**
   * Workload name in the project.
   */
  workload: string;
};

