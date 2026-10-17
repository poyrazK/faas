/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConfigChangeReceipt } from './ConfigChangeReceipt.js';
/**
 * An env var envelope: key name + scope + timestamps. The plaintext value never appears here.
 */
export type AppEnvResponse = {
  key: string;
  /**
   * Env-var scope (ADR-090). Always 'default' for the pre-PR-B flat response; varies for the nested `env_by_scope` response.
   */
  scope: string;
  created_at: string;
  updated_at: string;
  /**
   * Present on writes: the config revision recorded and the config guard watching it (ADR-1451).
   */
  config_change?: ConfigChangeReceipt;
};

