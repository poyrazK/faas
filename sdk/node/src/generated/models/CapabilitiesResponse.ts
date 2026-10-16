/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CapabilityStatus } from './CapabilityStatus.js';
import type { EnvironmentCacheScheduleCapabilities } from './EnvironmentCacheScheduleCapabilities.js';
/**
 * Account-scoped capability registry with resolved entitlement and existing runtime availability gates.
 */
export type CapabilitiesResponse = {
  /**
   * Explicit serving-backend schedule workflow support. Omission on older APIs means unknown; clients must not infer recovery support or emulate checkpoints by scanning the feed.
   */
  environment_cache_schedules?: EnvironmentCacheScheduleCapabilities;
  /**
   * The serving control plane and state backend support atomic deployment-guarded parking. Older servers omit this field, which means unsupported. This is a preflight signal, not a cluster-wide health guarantee; always use the conditional parking endpoint, including in mixed-version fleets.
   */
  conditional_parking?: boolean;
  /**
   * Version of the embedded product capability catalog.
   */
  registry_version: number;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  capabilities: Array<CapabilityStatus>;
};

