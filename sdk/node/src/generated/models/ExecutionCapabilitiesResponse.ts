/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionCapabilityLimits } from './ExecutionCapabilityLimits.js';
import type { ExecutionProfileCapability } from './ExecutionProfileCapability.js';
/**
 * Supported Runs admission contract for the authenticated account.
 * admission_available reports plan entitlement and the control-plane
 * API gate only. It does not report scheduler dispatcher or profile image
 * readiness.
 *
 */
export type ExecutionCapabilitiesResponse = {
  /**
   * Account plan identifier.
   */
  plan: string;
  /**
   * True when the account is entitled and the control-plane admission gate is enabled.
   */
  admission_available: boolean;
  /**
   * Whether the account plan allows Runs.
   */
  plan_entitled: boolean;
  /**
   * Whether the apid Runs admission gate is enabled.
   */
  control_plane_enabled: boolean;
  unavailable_reasons?: Array<'plan_not_entitled' | 'control_plane_disabled'>;
  runtimes: Array<'node22' | 'node24' | 'python312' | 'python313'>;
  profiles: Array<ExecutionProfileCapability>;
  network_modes: Array<'none'>;
  limits?: ExecutionCapabilityLimits;
};

