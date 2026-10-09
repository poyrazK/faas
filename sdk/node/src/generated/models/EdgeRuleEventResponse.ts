/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EdgeRuleEventResponse = {
  id: string;
  rule_id: string;
  /**
   * Empty once the rule is deleted.
   */
  rule_name?: string;
  rule_kind?: string;
  outcome: 'matched' | 'logged';
  occurred_at: string;
  request_id?: string;
  method?: string;
  host?: string;
  /**
   * Request path without the query string.
   */
  path?: string;
  /**
   * Trusted client IP
   */
  client_ip?: string;
  country?: string;
  user_agent?: string;
};

