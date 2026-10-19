/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperatorPlatformFlagConvergence } from './OperatorPlatformFlagConvergence.js';
import type { OperatorRuntimeConfigApplyReceipt } from './OperatorRuntimeConfigApplyReceipt.js';
import type { PlatformFlagDefinition } from './PlatformFlagDefinition.js';
/**
 * One catalogued platform flag with its live definition, apply receipts and convergence.
 */
export type OperatorPlatformFlag = {
  key: string;
  /**
   * Runtime-configuration key that stores the flag; write it with PATCH /v1/admin/config/{key}.
   */
  config_key: string;
  label: string;
  description: string;
  capability?: string;
  adr: string;
  consumers: Array<string>;
  legacy_env: Array<string>;
  retire_by?: string;
  definition: PlatformFlagDefinition;
  source: 'default_or_environment' | 'operator';
  version: number;
  status: 'pending' | 'applied' | 'failed' | 'blocked';
  last_error?: string;
  updated_at?: string;
  receipts: Array<OperatorRuntimeConfigApplyReceipt>;
  convergence: OperatorPlatformFlagConvergence;
};

