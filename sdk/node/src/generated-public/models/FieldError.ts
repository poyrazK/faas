/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-field entry of `Problem.errors`. The shape mirrors
 * Cloudflare's API Shield 422 / Stripe's `card_errors` family so
 * an SDK can iterate `errors[]` to drive form-field UI without
 * parsing prose. `field` uses JSON Pointer notation for nested
 * keys (`address.zip`). `expected` and `got` are short stable
 * strings; consumers should not depend on the prose.
 *
 */
export type FieldError = {
  field: string;
  expected: string;
  got?: string;
};

