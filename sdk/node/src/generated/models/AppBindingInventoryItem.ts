/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingApplicationAdoption } from './BindingApplicationAdoption.js';
import type { BindingRefresh } from './BindingRefresh.js';
import type { BindingVerification } from './BindingVerification.js';
import type { OutboundBindingProbePolicy } from './OutboundBindingProbePolicy.js';
/**
 * Public binding configuration with separately reported runtime observations and verification status.
 */
export type AppBindingInventoryItem = {
  type: 'service' | 'postgres' | 'object_storage' | 'queue' | 'outbound';
  name: string;
  /**
   * Logical binding name or environment key. Empty for outbound bindings.
   */
  binding: string;
  /**
   * Resource environment scope or app for an app-wide binding.
   */
  scope: string;
  access: string;
  /**
   * Configuration or provisioning state; never evidence of connectivity.
   */
  state: string;
  /**
   * Last-known queue consumer liveness (healthy, stale or degraded), otherwise unknown.
   */
  runtime_status: string;
  /**
   * Latest platform canary: passed, failed, unknown or stale. Deployment or configuration changes invalidate old evidence. Inventory does not run probes.
   */
  verification_status: string;
  verification?: BindingVerification;
  refresh?: BindingRefresh;
  application_adoption?: BindingApplicationAdoption;
  /**
   * Scheduler observation time. Absent when no observation exists.
   */
  observed_at?: string;
  http_url?: string;
  https_env?: string;
  https_url?: string;
  transport?: string;
  credential_generation?: number;
  rotation_pending?: boolean;
  consumer_state?: string;
  consumer_state_reason?: string;
  consumer_liveness?: string;
  outbound_probe?: OutboundBindingProbePolicy;
  credential_configured?: boolean;
  allowed_methods?: Array<string>;
  allowed_path_prefixes?: Array<string>;
};

