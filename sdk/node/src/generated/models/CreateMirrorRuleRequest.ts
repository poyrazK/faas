/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/mirrors. Both deployments must
 * be `live` and belong to the same app. `percent` defaults to
 * a 5% sample. `include_body` defaults to `false`; when enabled,
 * only response SHA-256 hashes are retained. Raw bodies are never
 * stored. POST, PUT, PATCH, and DELETE are skipped unless
 * `allow_unsafe_methods` is explicitly enabled.
 * `redact_headers` is the
 * customer's additive list on top of the always-stripped list
 * (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization,
 * WWW-Authenticate — applied by PR-A3's redaction layer, NOT by
 * A2's storage layer). Requests whose bodies exceed the 64 KiB mirror
 * snapshot cap are safely skipped rather than sending a partial request
 * to the mirror deployment.
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
   * Fan-out percent. Defaults to a 5% sample; 100 mirrors every eligible request.
   */
  percent?: number;
  /**
   * If true, compare response values using hashes. Raw response bodies are never stored. Off by default.
   */
  include_body?: boolean;
  /**
   * If true, also mirror POST, PUT, PATCH, and DELETE. These methods may cause side effects in the mirror deployment.
   */
  allow_unsafe_methods?: boolean;
  /**
   * Customer-supplied additional header names to redact on top of the always-stripped list (Authorization, Cookie, Set-Cookie, X-API-Key, Proxy-Authorization, WWW-Authenticate).
   */
  redact_headers?: Array<string>;
};

