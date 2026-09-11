/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CapabilityStatus } from './CapabilityStatus.js';
/**
 * Account-scoped capability registry and resolved plan entitlements.
 */
export type CapabilitiesResponse = {
  /**
   * Version of the embedded product capability catalog.
   */
  registry_version: number;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  capabilities: Array<CapabilityStatus>;
};

