/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Multipart body for POST /v1/projects/scan (dry-run).
 */
export type ProjectScanRequest = {
  /**
   * Preserve repository service identities and suggest project-prefixed app names. Requires no_triggers and no repository push binding; apply repeats the reviewed setting.
   */
  migration?: boolean;
  /**
   * Exact repository service names mapped to Gregale app names. Existing project members retain their names. Names are checked for format, reservation and availability; they are never allocated by scan. Multipart clients send this field as JSON.
   */
  app_slugs?: Record<string, string>;
  /**
   * tar.gz of the repo root.
   */
  source: Blob;
  /**
   * kebab slug; default = repo dir basename
   */
  project_slug?: string;
  /**
   * GitHub owner/name to persist for push reconciliation
   */
  repo_full_name?: string;
  production_branch?: string;
  /**
   * GitHub installation id (with --repository or --repo); 0 for unbound repos
   */
  install_id?: number;
  /**
   * CSV of workload names to include (others skipped)
   */
  only?: string;
  /**
   * Environment slug used to scope all discovered workloads
   */
  environment?: string;
  /**
   * Leave trigger and async-route declarations and existing project trigger/route state unchanged for this scan/apply pair.
   */
  no_triggers?: boolean;
  /**
   * Override the customer identity policy for selected workloads in this scan/apply pair (Hobby and above). Omitted uses Compose declarations and preserves existing app policy. Apply must repeat the same override as scan.
   */
  platform_tenant_required?: boolean;
  /**
   * Apply only: release frozen apps to production anyway; the reason is audited (ADR-20261019071733).
   */
  override_freeze?: string;
};

