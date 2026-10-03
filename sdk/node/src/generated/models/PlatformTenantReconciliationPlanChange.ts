/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One deterministic planned or applied entry. remove_candidate is advisory in a plan; a confirmed apply reports detached or removed.
 */
export type PlatformTenantReconciliationPlanChange = {
  resource_type: 'consumer' | 'surface' | 'hostname';
  action: 'create' | 'link' | 'keep' | 'remove_candidate' | 'retain_unmanaged' | 'created' | 'linked' | 'detached' | 'removed';
  /**
   * Existing resource ID; absent for a resource that would be created.
   */
  id?: string;
  app_id?: string;
  /**
   * Present for consumer entries.
   */
  external_ref?: string;
  /**
   * Present for consumer and surface entries.
   */
  name?: string;
  /**
   * Present for hostname entries on an existing surface.
   */
  surface_id?: string;
  /**
   * Present for hostname entries.
   */
  hostname?: string;
  /**
   * Existing provenance flag; omitted when the plan describes a resource that does not exist yet.
   */
  managed_by_platform_tenant?: boolean;
};

