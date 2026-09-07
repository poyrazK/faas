/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Effective build plan surfaced on DeploymentResponse (issue #961 / zero-config profile PR). Captured from the exact source archive at enqueue time and retained after spool cleanup; legacy rows fall back to marker detection when the spool is still available. Embedded on DeploymentResponse; never returned by a dedicated route.
 */
export type BuildPlan = {
  /**
   * Compatibility framework family derived from the persisted source profile. `unknown` means no supported framework was inferred (monorepo / custom build).
   */
  framework: 'node' | 'python' | 'go' | 'docker' | 'unknown';
  /**
   * Runtime the app is pinned to (eg `node22`, `python312`). Echoed from app.Runtime. nil for apps without a runtime set (image deploys).
   */
  runtime?: string | null;
  /**
   * Framework version extracted from the detected marker (eg `package.json` `engines.node`, `requirements.txt` head pin). nil when the marker has no version or framework is `unknown`.
   */
  version?: string | null;
  /**
   * Effective start command from the persisted source profile, replaced by an explicit entrypoint override when supplied.
   */
  entrypoint?: string | null;
  /**
   * Effective listen port from the persisted source profile, replaced by an explicit port override when supplied.
   */
  port?: number | null;
  /**
   * Effective readiness path selected by the source profile or deployment override.
   */
  health_path?: string | null;
  /**
   * App class from `app.Type` — `app` for plain apps, `function` for function rewrites (spec §4.2).
   */
  class?: 'app' | 'function';
};

