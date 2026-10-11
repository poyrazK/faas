/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create an edge-rule list (ADR-963).
 */
export type CreateEdgeRuleListRequest = {
  name: string;
  /**
   * ip (addresses and CIDRs, for client_ip), country (ISO 3166-1 alpha-2), host (exact hosts and *.suffix patterns), string (exact values, for path, header, cookie and query fields), asn (autonomous system numbers such as 13335 or AS13335, for asn).
   */
  kind: 'ip' | 'country' | 'host' | 'string' | 'asn';
  description?: string;
  items?: Array<string>;
};

