/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppBindingInventoryItem } from './AppBindingInventoryItem.js';
import type { BindingInventoryIssue } from './BindingInventoryIssue.js';
import type { BindingRuntimeFreshness } from './BindingRuntimeFreshness.js';
/**
 * Best-effort app binding metadata with explicit completeness and sanitized section issues.
 */
export type AppBindingInventory = {
  app: string;
  /**
   * Resource scope filter. Absent means all scopes.
   */
  scope?: string;
  generated_at: string;
  /**
   * Confirms that the explicit deployment_id selector was applied. Absent for default selection.
   */
  requested_deployment_id?: string;
  /**
   * Deployment selected for evidence: explicit selector or current manual-task deployment.
   */
  verification_deployment_id?: string;
  /**
   * Scope of the selected verification deployment.
   */
  verification_scope?: string;
  runtime_freshness?: BindingRuntimeFreshness;
  /**
   * All binding sections could be read; this does not mean that bindings are healthy or verified.
   */
  complete: boolean;
  bindings: Array<AppBindingInventoryItem>;
  issues?: Array<BindingInventoryIssue>;
  /**
   * Human-readable, sanitized messages for each issue.
   */
  warnings?: Array<string>;
};

