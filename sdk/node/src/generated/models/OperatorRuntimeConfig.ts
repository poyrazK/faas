/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperatorRuntimeConfigAck } from './OperatorRuntimeConfigAck.js';
/**
 * One entry from the closed operator runtime-configuration catalog.
 */
export type OperatorRuntimeConfig = {
  key: string;
  label: string;
  description: string;
  category: string;
  kind: 'boolean' | 'integer' | 'duration' | 'string' | 'enum' | 'secret_reference';
  default_value: any;
  desired_value: any;
  effective_value: any;
  scope: 'global' | 'control_plane' | 'daemon' | 'node';
  scope_id?: string;
  rollout_percent: number;
  rollout_state: 'stable' | 'canary' | 'promoting' | 'paused' | 'rolled_back';
  /**
   * Whether the safety controller automatically advances a healthy daemon canary.
   */
  auto_promote: boolean;
  source: 'default_or_environment' | 'operator';
  apply_mode: 'hot' | 'graceful' | 'rolling' | 'break_glass';
  controller_enabled: boolean;
  mutable: boolean;
  sensitive: boolean;
  status: 'pending' | 'applied' | 'failed' | 'blocked';
  last_error?: string;
  version: number;
  updated_at?: string;
  applied_at?: string;
  /**
   * Per-daemon observations of the requested configuration version.
   */
  acks?: Array<OperatorRuntimeConfigAck>;
};

