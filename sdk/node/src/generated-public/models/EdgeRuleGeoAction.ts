/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ISO 3166-1 alpha-2 country allow/deny evaluator (ADR-091
 * D21 / §4.1.2.8b). Mirrors `EdgeRuleIPAction` exactly: `allow`
 * and `deny` are parallel ISO 3166-1 alpha-2 country-code
 * lists; the matcher walks deny AFTER allow so a single-country
 * deny wins even when the allow list is broad.
 *
 * The match port is `gatewayd-internal` which consults a
 * DB-IP Lite `.mmdb` file at request time to translate the
 * trusted XFF client IP into a country code. Plan-tier quota
 * is enforced at apid-create time via `Limits.EdgeRulesGeoPerApp`
 * (Free=1, Hobby=5, Pro=25, Scale=100) inside the same apps-row
 * FOR UPDATE lock as the general edge-rule cap. Geo is NOT in
 * IsPaidOnly — Free customers get one rule before they upgrade.
 *
 * Failure posture is fail-open: missing `.mmdb`, IP not in
 * any country, RFC1918/bogon, or corrupt file → the rule does
 * not fire → the request flows through. The
 * `gateway_edge_rule_match_total{kind="geo",result="failed"}`
 * counter increments and an `edge_rule.geo_failed` audit event
 * emits.
 *
 */
export type EdgeRuleGeoAction = {
  /**
   * ISO 3166-1 alpha-2 country allowlist. Empty + deny non-empty = deny-only. Empty + deny empty = no-op (create-time 422).
   */
  allow?: Array<string>;
  /**
   * ISO 3166-1 alpha-2 country denylist. Evaluated AFTER allow; single-country deny wins.
   */
  deny?: Array<string>;
};

