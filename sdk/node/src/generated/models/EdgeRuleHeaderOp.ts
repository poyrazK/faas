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
   * Expand ${name} request values in `value` (host, path, method, query, client_ip, country, asn, request_id, header:<name>, query:<name>, cookie:<name>;
   * $$ is a literal $). Control characters are dropped and each
   * value is capped at 1 KiB. Not allowed with action=remove.
   *
   */
  template?: boolean;
  /**
   * Seal `value` (request headers only): it is
   * stored encrypted to the edge seal domain and never returned.
   * Authorization, Proxy-Authorization, Cookie and names containing
   * token, secret, password, api-key, apikey, signature or credential
   * are sealed without it. Not allowed with template or action=remove.
   *
   */
  secret?: boolean;
  /**
   * The sealed value of a secret op, returned in place of `value`.
   * Send it back with no `value` to keep that value.
   *
   */
  secret_id?: string;
  /**
   * Set in responses on a credential-named value stored before
   * sealing, which is left out. Send the op back with no `value` to
   * seal the stored value.
   *
   */
  redacted?: boolean;
};

