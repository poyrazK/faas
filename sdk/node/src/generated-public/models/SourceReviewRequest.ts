/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Git repository and revision selectors to resolve without creating a deployment.
 */
export type SourceReviewRequest = {
  repo: string;
  /**
   * GitHub branch or full lowercase commit SHA; use the commit SHA for tags.
   */
  ref: string;
  /**
   * Registered target environment; omitted selects default.
   */
  environment?: string;
};

