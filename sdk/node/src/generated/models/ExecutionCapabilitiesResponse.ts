/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionCapabilityLimits } from './ExecutionCapabilityLimits.js';
import type { ExecutionClassCapability } from './ExecutionClassCapability.js';
import type { ExecutionEnvironmentLimits } from './ExecutionEnvironmentLimits.js';
import type { ExecutionFileLimits } from './ExecutionFileLimits.js';
import type { ExecutionProfileCapability } from './ExecutionProfileCapability.js';
import type { RunControlCapabilities } from './RunControlCapabilities.js';
/**
 * Supported Runs admission contract for the authenticated account.
 * admission_available reports plan entitlement and the control-plane
 * API gate only. It does not report scheduler dispatcher or profile image
 * readiness.
 *
 */
export type ExecutionCapabilitiesResponse = {
  /**
   * New Run artifact deliveries are currently admitted into configured app upload routes.
   */
  artifact_deliveries?: boolean;
  /**
   * Whether new version-pinned task pipelines and batches are enabled.
   */
  task_pipelines?: boolean;
  run_controls?: RunControlCapabilities;
  custom_environments?: boolean;
  environment_runtimes?: Array<'node22' | 'node24' | 'python312' | 'python313'>;
  environment_limits?: ExecutionEnvironmentLimits;
  execution_classes?: Array<ExecutionClassCapability>;
  /**
   * New standalone contract and stored-file admissions are enabled.
   */
  extended_admissions?: boolean;
  file_limits?: ExecutionFileLimits;
  max_contract_schema_bytes?: number;
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
   * Whether the API Runs admission gate is enabled.
   */
  control_plane_enabled: boolean;
  unavailable_reasons?: Array<'plan_not_entitled' | 'control_plane_disabled'>;
  runtimes: Array<'node22' | 'node24' | 'python312' | 'python313'>;
  profiles: Array<ExecutionProfileCapability>;
  network_modes: Array<'none'>;
  limits?: ExecutionCapabilityLimits;
};

