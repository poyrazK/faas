/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectMigrationBuildWait } from './ProjectMigrationBuildWait.js';
/**
 * Progress and historical evidence pointers for one migration service and its current attempt.
 */
export type ProjectMigrationService = {
  /**
   * Saved declared API services required before this migration service builds.
   */
  build_dependencies?: Array<string>;
  build_wait?: ProjectMigrationBuildWait;
  /**
   * Saved public build variable-to-service choices.
   */
  public_service_connections?: Record<string, string>;
  /**
   * Explicitly reviewed public HTTPS URLs. Saved values may appear in browser bundles; credentials, queries and fragments are rejected.
   */
  public_build_urls?: Record<string, string>;
  /**
   * Selected workload name.
   */
  name: string;
  /**
   * Current application slug.
   */
  app?: string;
  /**
   * Application identity bound before enqueue.
   */
  app_id?: string;
  /**
   * Exact reserved deployment for this attempt.
   */
  deployment_id?: string;
  /**
   * Exact reserved build for this attempt.
   */
  build_id?: string;
  attempt: number;
  /**
   * Observed state of the reserved deployment, waiting for build APIs, pending work or changed application.
   */
  status: string;
  /**
   * Metadata-only explanation for unfinished work.
   */
  reason?: string;
  depends_on: Array<string>;
  required_env_keys: Array<string>;
  configured_keys: Array<string>;
  /**
   * Saved runtime variable names mapped to selected declared services. Resumed automatically; contains no values.
   */
  service_connections?: Record<string, string>;
  /**
   * Latest recorded verification identity for this exact deployment.
   */
  verification_id?: string;
  /**
   * Last recorded verification result; reopen verification to validate current configuration.
   */
  verification_status?: string;
  /**
   * Latest saved cutover identity for this exact deployment.
   */
  cutover_id?: string;
  /**
   * Last recorded cutover state; reopen cutover to validate current configuration.
   */
  cutover_status?: string;
};

