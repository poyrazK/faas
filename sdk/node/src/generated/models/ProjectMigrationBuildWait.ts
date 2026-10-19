/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable readiness progress before a migration build consumes a builder slot. Public URLs still route normally; this is a point-in-time readiness check.
 */
export type ProjectMigrationBuildWait = {
  /**
   * Whether selected API deployments satisfied build admission.
   */
  status: 'waiting' | 'ready' | 'failed';
  /**
   * Stable build dependency blocker code, when present.
   */
  code?: string;
  /**
   * Customer-safe explanation of a build dependency wait or failure.
   */
  detail?: string;
  /**
   * Fixed deadline for this attempt's API readiness wait.
   */
  deadline_at: string;
};

