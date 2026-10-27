/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConfigGuardDecision } from './ConfigGuardDecision.js';
/**
 * A scope's config guard.
 */
export type ConfigGuard = {
  id: string;
  scope: string;
  status: 'watching' | 'clean' | 'restored' | 'regressed' | 'no_traffic' | 'superseded';
  base_revision: number;
  head_revision: number;
  restorable: boolean;
  opened_at: string;
  last_change_at: string;
  window_ends_at?: string;
  closed_at?: string;
  decision?: ConfigGuardDecision;
};

