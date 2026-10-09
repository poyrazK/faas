/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type CreateEdgeRuleListRequest = {
  name: string;
  /**
   * ip (addresses and CIDRs, for client_ip), country (ISO 3166-1 alpha-2), host (exact hosts and *.suffix patterns), string (exact values, for path, header, cookie and query fields).
   */
  kind: 'ip' | 'country' | 'host' | 'string';
  description?: string;
  items?: Array<string>;
};

