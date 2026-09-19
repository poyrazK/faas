/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provider-neutral private-network allow rule.
 */
export type PrivateNetworkFirewallRule = {
  direction: 'ingress' | 'egress';
  protocol: 'tcp' | 'udp' | 'icmp';
  /**
   * Source CIDRs for ingress or destination CIDRs for egress; empty means the network CIDR.
   */
  cidrs?: Array<string>;
  /**
   * TCP/UDP ports or inclusive ranges such as 443 or 8000-8080; omit for ICMP.
   */
  ports?: Array<string>;
};

