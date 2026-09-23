/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request to register or clone a named project environment.
 */
export type CreateProjectEnvironmentRequest = {
  /**
   * Project environment slug; the reserved app scope `default` cannot be used.
   */
  slug: string;
  protected?: boolean;
  /**
   * Source environment whose scoped configuration and values are copied.
   */
  from_environment?: string;
  /**
   * Explicitly attach fresh target-scoped credentials to the source environment's managed database and object-storage resources; data remains shared.
   */
  share_resources?: boolean;
};

