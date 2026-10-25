/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperatorPlatformFlagConvergence } from './OperatorPlatformFlagConvergence.js';
import type { OperatorPlatformFlagOptIns } from './OperatorPlatformFlagOptIns.js';
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
  /**
   * Subject attributes every consumer can supply, so rules may constrain them; a write using any other is refused (ADR 20261021080640).
   */
  attributes: Array<'account' | 'plan' | 'org'>;
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
  opt_ins: OperatorPlatformFlagOptIns;
  /**
   * Tier of the capability the flag releases (ADR-20261023115741).
   */
  maturity?: 'ga' | 'beta' | 'preview' | 'internal';
  /**
   * Parent capability flag; a sub-feature is released only while its parent is.
   */
  parent?: string;
  /**
   * Core capability flag: turning it off for every account needs force.
   */
  core?: boolean;
  /**
   * What the flag gates today, joined with +: routes, settings, daemons; or handler or none.
   */
  enforcement?: string;
};

