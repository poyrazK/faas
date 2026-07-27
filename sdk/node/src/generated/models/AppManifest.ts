/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-as-source flag (§ux 6.3).
 */
export type AppManifest = {
  entrypoint: Array<string>;
  env?: Record<string, string>;
  working_dir?: string | null;
  port?: number | null;
  healthz?: string | null;
  user?: string | null;
};

