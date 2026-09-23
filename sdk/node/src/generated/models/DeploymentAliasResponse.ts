/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A customer's named pointer to one per-app deployment revision, including its stable host when the platform apps domain is configured.
 */
export type DeploymentAliasResponse = {
  name: string;
  deployment_id: string;
  /**
   * Per-app revision number; rendered in CLI output as vN.
   */
  revision: number;
  /**
   * Stable one-label hostname for this alias. It is keyed by an immutable app identifier and remains stable if the app slug is renamed.
   */
  host?: string;
  /**
   * HTTPS URL for the alias host; omitted when the platform apps domain is not configured.
   */
  url?: string;
  created_at: string;
  updated_at: string;
};

