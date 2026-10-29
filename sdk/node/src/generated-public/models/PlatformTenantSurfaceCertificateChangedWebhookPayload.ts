/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Persisted certificate-state transition for a surface explicitly linked to this tenant. Raw provider error text, certificates, and private keys are never included; use the activation snapshot for current diagnostic details.
 */
export type PlatformTenantSurfaceCertificateChangedWebhookPayload = {
  platform_tenant_id: string;
  /**
   * The platform's durable identifier for this customer record.
   */
  external_ref: string;
  surface_id: string;
  surface_name: string;
  app_id: string;
  cert_state: 'none' | 'pending' | 'issued' | 'failed';
  /**
   * Null unless a certificate expiry is recorded.
   */
  cert_not_after: string | null;
  changed_at: string;
};

