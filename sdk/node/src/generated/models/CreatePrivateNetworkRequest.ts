/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PrivateNetworkFirewallRule } from './PrivateNetworkFirewallRule.js';
/**
 * POST /v1/networks body for a Gregale-owned network.
 */
export type CreatePrivateNetworkRequest = {
  name: string;
  region: string;
  cidr: string;
  /**
   * Optional reusable CIDR allowlist contained by cidr.
   */
  allowed_cidrs?: Array<string>;
  /**
   * Optional protocol/port allow rules contained by cidr.
   */
  firewall_rules?: Array<PrivateNetworkFirewallRule>;
};

