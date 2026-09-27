/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bind a custom domain to an app, optionally routing it to one project environment.
 */
export type CreateCustomDomainRequest = {
  domain: string;
  app_id: string;
  /**
   * Optional project environment slug. When set, traffic follows only that environment's active release graph.
   */
  environment?: string;
};

