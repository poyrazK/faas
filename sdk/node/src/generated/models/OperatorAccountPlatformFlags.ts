/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperatorPlatformFlagDecision } from './OperatorPlatformFlagDecision.js';
/**
 * Every platform flag decided for one account.
 */
export type OperatorAccountPlatformFlags = {
  account_id: string;
  plan: string;
  items: Array<OperatorPlatformFlagDecision>;
};

