/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PrivateNetworkFirewallRule } from './PrivateNetworkFirewallRule.js';
/**
 * PUT /v1/networks/{id}/policy body.
 */
export type UpdatePrivateNetworkPolicyRequest = {
  /**
   * CIDRs reachable by attached workloads; empty disables the CIDR restriction.
   */
  allowed_cidrs: Array<string>;
  /**
   * Protocol/port allow rules contained by the network CIDR; empty disables the rule restriction.
   */
  firewall_rules?: Array<PrivateNetworkFirewallRule>;
};

