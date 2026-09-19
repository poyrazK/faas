/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PrivateNetworkFirewallRule } from './PrivateNetworkFirewallRule.js';
/**
 * Gregale-owned private-network definition.
 */
export type PrivateNetwork = {
  id: string;
  name: string;
  region: string;
  /**
   * Canonical IPv4 RFC1918 range, /16 through /28.
   */
  cidr: string;
  /**
   * Optional reusable CIDR allowlist for all attached workloads.
   */
  allowed_cidrs?: Array<string>;
  /**
   * Optional protocol/port allow rules applied to every attachment.
   */
  firewall_rules?: Array<PrivateNetworkFirewallRule>;
  status: 'ready' | 'error';
  status_detail?: string;
  created_at?: string;
  updated_at?: string;
};

