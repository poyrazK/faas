/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Frozen deployment service edge and current reviewed route metadata. Only the named managed verification contract can qualify this context; ordinary app receipts cannot.
 */
export type ServiceBindingInventoryContext = {
  source: 'environment_release';
  caller_deployment_id: string;
  /**
   * Account-owned frozen target app; omitted when unreadable or foreign.
   */
  target_app_id?: string;
  /**
   * Physical target app slug; the binding name remains the logical workload.
   */
  target_app?: string;
  /**
   * Active reviewed release selected by the managed route resolver, when available.
   */
  release_id?: string;
  /**
   * Opaque digest of reviewed graph identities and public edge facts; changes invalidate a verification review. Not a connectivity receipt or authorization capability.
   */
  revision: string;
  routing_status: 'resolved' | 'denied' | 'unavailable';
  /**
   * Stable sanitized reason for managed route resolution.
   */
  routing_reason: string;
  /**
   * True only for an active request/service or worker caller with a supported frozen HTTPS edge to a request/service target.
   */
  verification_supported: boolean;
  /**
   * managed-https-v1 identifies the supported managed guest probe contract. Missing or unknown contracts remain unsupported.
   */
  verification_contract?: string;
  /**
   * Empty for a supported managed contract; otherwise managed_service_verification_unsupported.
   */
  verification_reason: string;
};

