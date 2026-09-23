/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One expected preview workload and its latest deployment for the recorded commit.
 */
export type PreviewEnvironmentMemberResponse = {
  app_id: string;
  /**
   * Empty if the recorded app is missing.
   */
  slug: string;
  workload_name: string;
  /**
   * Includes missing when the recorded app is unavailable.
   */
  app_status: string;
  preview_state: string;
  /**
   * Empty until a deployment for the recorded commit exists.
   */
  deployment_id: string;
  /**
   * Missing until a deployment for the recorded commit exists.
   */
  deployment_status: string;
};

