/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One header mutation. `action` ∈ {add,set,remove}.
 */
export type EdgeRuleHeaderOp = {
  name: string;
  value?: string;
  action: 'add' | 'set' | 'remove';
  /**
   * ADR-967. Expand ${name} request values in `value` (host, path, method, query, client_ip, country, asn, request_id, header:<name>, query:<name>, cookie:<name>;
   * $$ is a literal $). Control characters are dropped and each
   * value is capped at 1 KiB. Not allowed with action=remove.
   *
   */
  template?: boolean;
};

