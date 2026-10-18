/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained deployment-bound receipt for an explicitly authorized push queue completion probe.
 */
export type QueueBindingCanary = {
  id: string;
  binding: string;
  deployment_id: string;
  scope: string;
  status: 'published' | 'dispatching' | 'succeeded' | 'failed' | 'blocked' | 'expired' | 'cancelled';
  reason?: string;
  published_at: string;
  expires_at: string;
  delivered_at?: string;
  completed_at?: string;
};

