/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConfigChangeReceipt } from './ConfigChangeReceipt.js';
import type { ConfigSecretDifference } from './ConfigSecretDifference.js';
/**
 * What a config rollback changed, and the secrets it could not restore.
 */
export type ConfigRollbackResponse = {
  restored_revision: number;
  change?: ConfigChangeReceipt;
  set: Array<string>;
  deleted: Array<string>;
  secrets_not_restored: Array<ConfigSecretDifference>;
};

