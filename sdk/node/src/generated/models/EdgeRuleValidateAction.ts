/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleValidateParameters } from './EdgeRuleValidateParameters.js';
/**
 * Customer-supplied JSON Schema (Draft 2020-12) evaluated against
 * the inbound request body BEFORE the wake gate fires. The
 * kind=validate rule is the platform's API-native request-
 * validation surface: rejections return 422
 * `request_validation_failed` with `Problem.errors` carrying
 * per-field detail, never pay a cold-boot cost, never consume
 * the rate-limit / wake-quota budget on malformed traffic.
 *
 * Free-and-above (no plan gate). Schema lives inline in the
 * `action` jsonb blob (single-table flow), capped at the
 * platform's `MaxEdgeRuleValidateSchemaBytes` (64 KiB). The
 * gateway re-validates at compile time as defence-in-depth so
 * the SQL hotfix path that bypasses apid-Validate still cannot
 * ship an external `$ref`.
 *
 * Field-by-field:
 * * `schema` — JSON Schema document (Draft 2020-12) for the
 * request body. Capped at 64 KiB. External `$ref` / `$id` URLs
 * are rejected at create-time; internal pointers
 * (`#/definitions/Foo`) pass through. Optional when
 * `parameters` is set, so a body-less request can be validated.
 * * `parameters` — optional path, query, and header schemas,
 * checked before the body is read (ADR-091 amendment).
 * * `content_types` — optional media-type allowlist.
 * Closed set `application*`. Empty = match any.
 * * `apply_while_streaming` — per-rule opt-in for the
 * streaming response path (ADR-047). Default false mirrors
 * the §4.1 `Accept: application/json` opt-out.
 * * `reject_on_unknown_fields` — toggles
 * `additionalProperties: false` on the compiled schema.
 * Default false preserves byte-stable schemas.
 * * `max_body_bytes` — per-rule inbound body cap. 0 =
 * inherit `MaxRequestBodyBytes` (per-plan 25 MB buffered /
 * 100 MB streaming). Must be > 0 and <= `MaxRequestBodyBytes`.
 * At least one of `schema` and `parameters` is required.
 *
 */
export type EdgeRuleValidateAction = {
  /**
   * Inline JSON Schema document (Draft 2020-12). The schema
   * is preserved byte-exact across apid↔gatewayd round-trips
   * so the SHA-256 cache key in `pkg/edgevalidate` is stable.
   *
   */
  schema?: Record<string, any>;
  parameters?: EdgeRuleValidateParameters;
  /**
   * Optional media-type allowlist. Every entry must start
   * with `application/`. Empty array = match any Content-Type.
   *
   */
  content_types?: Array<string>;
  /**
   * Whether validation fires on the streaming response path
   * (ADR-047). Default false; set true per-rule to opt the
   * SSE / chunked response path into body validation.
   *
   */
  apply_while_streaming?: boolean;
  /**
   * Toggles `additionalProperties: false` on the compiled
   * schema. Default false so a body with stray fields does
   * not silently fail; opt in per-rule for strict schemas.
   *
   */
  reject_on_unknown_fields?: boolean;
  /**
   * Per-rule inbound body cap. 0 (default) inherits the
   * platform cap (`api.MaxRequestBodyBytes`). When set, must
   * be > 0 and <= the platform cap.
   *
   */
  max_body_bytes?: number;
  /**
   * DEPRECATED (ADR-128 D2). Moved to top-level
   * `EdgeRuleResponse.validate_mode` /
   * `CreateEdgeRuleRequest.validate_mode` /
   * `UpdateEdgeRuleRequest.validate_mode`. Retained on
   * the action body for the back-compat read window.
   * How the gateway handles a schema mismatch. `block`
   * rejects with 422 (the strictest mode; preserves the
   * pre-2026 behaviour). `observe` counts via the metric
   * and proxies normally. `warn` does the same and stamps
   * `X-Validation-Warning: <rule_id>` on the response.
   * An empty / omitted value is coerced to `block` at the
   * gateway-side handler.
   *
   * @deprecated
   */
  validate_mode?: 'block' | 'observe' | 'warn';
};

