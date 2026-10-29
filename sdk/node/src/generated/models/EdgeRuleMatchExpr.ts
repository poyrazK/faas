/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Match condition, ANDed with the rule's fixed selectors. A node
 * is exactly one of all, any, not, or a field/op leaf. Fields: method,
 * path, host, client_ip, country, asn (the client IP's
 * autonomous system, e.g. 13335 or AS13335), ua_family (the
 * User-Agent's family, one of browser, mobile, bot, tool, other),
 * verified_bot (a known crawler confirmed by its operator's
 * published IP ranges or forward-confirmed reverse DNS of the client
 * IP, one of googlebot, bingbot, applebot, yandexbot, baiduspider,
 * yahoo, petalbot; absent otherwise), ai_crawler (the AI crawler the User-Agent claims, such as gptbot, claudebot,
 * ccbot, perplexitybot, bytespider), ai_crawler_purpose (that
 * crawler's purpose: training, search or assistant),
 * verified_ai_crawler (the claimed AI crawler when the client IP is
 * in its operator's published ranges; only crawlers that publish
 * ranges can verify), header:<name>, cookie:<name>, query:<name>.
 * ua_family, verified_bot and the AI crawler fields take eq, ne, in,
 * not_in, exists, missing. Ops: eq, ne, in, not_in, prefix, suffix, contains,
 * exists, missing, regex (RE2), cidr (client_ip only), in_list (list names an account list whose kind fits the field). Depth at most 4,
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

