/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An explicit cache override in another named environment of the same app and project, pinned for an atomic one-time copy.
 */
export type EnvironmentCachePolicySource = {
  /**
   * Registered source environment slug; must differ from the destination.
   */
  scope: string;
  environment_id: string;
  revision: number;
};

