/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A failed deployment with the error and fix hint the build or rollout reported.
 */
export type AccountHealthDeployment = {
  /**
   * Failed deployment identifier.
   */
  deployment_id: string;
  /**
   * Identifier of the deployment's app.
   */
  app_id: string;
  /**
   * Slug of the deployment's app.
   */
  app_slug?: string;
  /**
   * Stable failure code.
   */
  error_code?: string;
  /**
   * Failure message, at most 500 bytes.
   */
  error?: string;
  /**
   * Suggested fix, at most 500 bytes.
   */
  hint?: string;
  /**
   * When the deployment was created.
   */
  created_at: string;
};

