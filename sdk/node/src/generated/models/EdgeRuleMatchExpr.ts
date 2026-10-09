/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-906 match condition, ANDed with the rule's fixed selectors. A node
 * is exactly one of all, any, not, or a field/op leaf. Fields: method,
 * path, host, client_ip, country, asn (ADR-910: the client IP's
 * autonomous system, e.g. 13335 or AS13335), header:<name>,
 * cookie:<name>, query:<name>. Ops: eq, ne, in, not_in, prefix, suffix, contains,
 * exists, missing, regex (RE2), cidr (client_ip only), in_list (ADR-907:
 * list names an account list whose kind fits the field). Depth at most 4,
 * at most 32 nodes, 64 values per leaf, values and regexes at most 256
 * bytes. An untrusted client IP, or its unknown country or ASN, is absent.
 *
 */
export type EdgeRuleMatchExpr = {
  all?: Array<EdgeRuleMatchExpr>;
  any?: Array<EdgeRuleMatchExpr>;
  not?: EdgeRuleMatchExpr;
  field?: string;
  op?: 'eq' | 'ne' | 'in' | 'not_in' | 'prefix' | 'suffix' | 'contains' | 'exists' | 'missing' | 'regex' | 'cidr' | 'in_list';
  value?: string;
  values?: Array<string>;
  /**
   * Account list name for op in_list.
   */
  list?: string;
};

