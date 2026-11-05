/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Ephemeral owner-authorized HTTP evidence; does not establish public ingress access.
 */
export type DeploymentResponseCheckResult = {
  app_id: string;
  deployment_id: string;
  scope: string;
  project_id?: string;
  environment_id?: string;
  instance_id: string;
  http_status: number;
  /**
   * UTF-8 response preview bounded to 4096 bytes.
   */
  body: string;
  body_truncated: boolean;
};

