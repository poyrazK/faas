/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 3xx short-circuit.
 */
export type EdgeRuleRedirectAction = {
  status_code: 301 | 302 | 307 | 308;
  to: string;
  /**
   * Headers stamped on the redirect response.
   */
  headers?: Record<string, string>;
  /**
   * ADR-967. Expand ${name} request values in `to` and in the
   * header values (host, path, method, query, client_ip, country, asn, request_id, header:<name>, query:<name>, cookie:<name>; $$ is a
   * literal $). The target must start with a literal "/" or
   * "http(s)://", and only ${host} may appear before the path.
   *
   */
  template?: boolean;
};

