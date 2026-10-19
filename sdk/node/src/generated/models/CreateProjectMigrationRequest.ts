/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectInitialSecrets } from './ProjectInitialSecrets.js';
import type { ProjectPublicBuildURLs } from './ProjectPublicBuildURLs.js';
import type { ProjectServiceConnections } from './ProjectServiceConnections.js';
/**
 * Start one durable migration of selected reviewed services with request-only configuration.
 */
export type CreateProjectMigrationRequest = {
  /**
   * Client-generated identity saved before submission.
   */
  id: string;
  /**
   * Reviewed migration-mode source token; never persisted in the receipt.
   */
  plan_token: string;
  only: Array<string>;
  initial_secrets?: ProjectInitialSecrets;
  service_connections?: ProjectServiceConnections;
  /**
   * Explicit public build URL mappings to selected declared dependencies. Resolves their reviewed public_url and saves it for retries. Public URL keys only.
   */
  public_service_connections?: ProjectServiceConnections;
  public_build_urls?: ProjectPublicBuildURLs;
  /**
   * Optional caller-to-API build waits. Targets must be selected declared dependencies. Each attempt pins the saved API deployment, waits up to 30 minutes without using a builder slot, and requires it to be live with 100 percent traffic. Resume restores these choices.
   */
  build_dependencies?: Record<string, Array<string>>;
};

