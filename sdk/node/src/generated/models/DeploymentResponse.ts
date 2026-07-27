/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps.
 */
export type DeploymentResponse = {
  id: string;
  app_id: string;
  build_id?: string | null;
  image_digest: string;
  kind: string;
  status: string;
  error?: string | null;
  error_code?: string | null;
  created_at: string;
};

