/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * HTTP GET probe sent from inside the container.
 */
export type SidecarHTTPGetProbe = {
  /**
   * HTTP path; defaults to /.
   */
  path?: string;
  /**
   * HTTP container port; 0/omitted inherits the workload port.
   */
  port?: number;
};

