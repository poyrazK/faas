/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/mirrors. Both deployments must
 * be `live` and belong to the same app. `include_body` defaults
 * to `false`; enabling it stores only request/response SHA-256
 * hashes for body-difference classification, never raw bodies.
 * `redact_headers` is the
 * customer's additive list on top of the always-stripped list
 * (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization,
 * WWW-Authenticate — applied by PR-A3's redaction layer, NOT by
 * A2's storage layer).
 *
 */
export type CreateMirrorRuleRequest = {
  /**
   * Source deployment id (live; must belong to the slug's app).
   */
  source_deployment_id: string;
  /**
   * Mirror deployment id (live; must belong to the slug's app; must differ from source).
   */
  mirror_deployment_id: string;
  /**
   * Fan-out percent. 100 = mirror every customer request; lower = sampled shadow.
   */
  percent?: number;
  /**
   * If true, the comparison ledger captures request/response SHA-256 hashes for body-difference classification. Raw bodies are never stored. Off by default.
   */
  include_body?: boolean;
  /**
   * Customer-supplied additional header names to redact on top of the always-stripped list (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization, WWW-Authenticate).
   */
  redact_headers?: Array<string>;
};

