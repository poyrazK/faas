/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Independent ownership, DNS routing, and TLS activation state.
 */
export type DomainReadiness = {
  status: 'awaiting_ownership' | 'expired' | 'checking_dns' | 'fix_routing' | 'preparing_https' | 'ready' | 'needs_attention';
  ownership: 'pending' | 'verified' | 'expired';
  routing: 'ok' | 'fail' | 'pending' | 'na';
  /**
   * Certificate state, independently of ownership and routing.
   */
  tls: string;
};

