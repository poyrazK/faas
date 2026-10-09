/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current app-owned queue diagnostics independent of the historical health window. Each waiting run has one primary reason; future wakes take precedence followed by app, tenant and workflow capacity. No payloads or tenant identities, global worker occupancy or completion estimates are returned.
 */
export type AutomationQueueHealth = {
  observed_at: string;
  /**
   * Current pending and parked runs plus expired running leases for this automation; reason counts sum to this value.
   */
  waiting_run_count: number;
  /**
   * Due waiting runs including capacity blockers and lease recovery.
   */
  due_run_count: number;
  /**
   * Running automation runs whose dispatch lease has expired.
   */
  stale_run_count: number;
  /**
   * Age since the oldest due run became eligible, bounded by creation time; zero if none is due.
   */
  oldest_due_age_seconds: number;
  /**
   * Live automation dispatch claims across all workflows and tenants in this app; excludes expired leases and native operation custody.
   */
  app_running_count: number;
  app_dispatch_limit: number;
  /**
   * Live claim limit per tenant within this app.
   */
  tenant_dispatch_limit: number;
  app_at_capacity: boolean;
  reason_counts: {
    /**
     * Due and passes observed run admission checks; does not guarantee immediate handler execution.
     */
    ready: number;
    /**
     * Future scheduling deadline that is not a pending step retry.
     */
    scheduled: number;
    /**
     * A pending step retry deadline equals the future persisted run wake.
     */
    retry_backoff: number;
    /**
     * Intentional future wait or timeout; inspect steps for timer, condition, event or callback details.
     */
    parked_wait: number;
    app_capacity: number;
    tenant_capacity: number;
    /**
     * The captured workflow definition run budget is full.
     */
    workflow_capacity: number;
  };
};

