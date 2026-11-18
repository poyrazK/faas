/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConfigChangeReceipt } from './ConfigChangeReceipt.js';
/**
 * An env var envelope: key name + scope + timestamps. The plaintext value appears only on a `?values=true` listing.
 */
export type AppEnvResponse = {
  key: string;
  /**
   * Env-var scope. Always 'default' for the legacy flat response; varies for the nested `env_by_scope` response.
   */
  scope: string;
  created_at: string;
  updated_at: string;
  /**
   * The variable's plaintext; present only with `?values=true`.
   */
  value?: string;
  /**
   * Present on env writes: the revision this write recorded and the config guard watching it.
   */
  config_change?: ConfigChangeReceipt;
};

