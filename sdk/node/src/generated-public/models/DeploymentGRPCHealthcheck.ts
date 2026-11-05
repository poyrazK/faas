/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Standard gRPC health.v1 readiness probe. An omitted or empty service checks overall server health.
 */
export type DeploymentGRPCHealthcheck = {
  /**
   * Service passed to the health.v1 Check request; omit it to query the server rather than a named service.
   */
  service?: string;
};

