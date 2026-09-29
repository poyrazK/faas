/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Deliberately small inventory projection for an organization app.
 * Does not expose creator identity, environment, secrets, or app
 * configuration while app-specific routes remain creator-scoped.
 *
 */
export type OrgAppSummary = {
  id: string;
  slug: string;
  type: 'app' | 'function';
  /**
   * Runtime identifier for functions when configured.
   */
  runtime?: string;
  status: string;
  created_at: string;
};

