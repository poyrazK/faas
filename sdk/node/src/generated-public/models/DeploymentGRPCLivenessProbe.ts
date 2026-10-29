/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Standard gRPC health.v1 liveness probe. An omitted or empty service checks overall server health.
 */
export type DeploymentGRPCLivenessProbe = {
  /**
   * Service passed to the health.v1 Check request; omit it to query overall server health.
   */
  service?: string;
};

