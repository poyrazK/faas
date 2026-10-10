/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Status and result for customer work; omits account, deployment, original payload and headers.
 */
export type PlatformTenantInvocationResponse = {
  id: string;
  state: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled' | 'dead_letter';
  method: string;
  path: string;
  attempts: number;
  /**
   * JSON result returned by the guest, when available.
   */
  result?: any;
  last_error?: string;
  outcome?: string | null;
  created_at: string;
  completed_at?: string | null;
};

