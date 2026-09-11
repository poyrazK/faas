/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-scoped GitHub installation and app binding health. Sealed
 * installation credentials are never returned. `state` is
 * `not_installed`, `installed`, or `bound`; `health` is
 * `not_connected`, `unknown`, `healthy`, or `degraded`.
 *
 */
export type GitHubInstallStatus = {
  state: 'not_installed' | 'installed' | 'bound';
  health: 'not_connected' | 'unknown' | 'healthy' | 'degraded';
  connected: boolean;
  installation_id?: number;
  github_login?: string;
  default_branch?: string;
  repo_full_name?: string;
  production_branch?: string;
  binding_id?: string;
  linked_at?: string | null;
  last_reconciled_at?: string | null;
  last_reconcile_error?: string;
  last_reconcile_repository_count: number;
  last_reconcile_detached_count: number;
  /**
   * CSRF token for sync and disconnect mutations.
   */
  csrf_token?: string;
  sync_result?: {
    detached?: boolean;
    remote_repository_count?: number;
    synced_at?: string;
  };
};

