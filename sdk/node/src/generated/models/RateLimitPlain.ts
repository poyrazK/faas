/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Plain-text body returned ONLY by the authlimiter middleware
 * (`pkg/middleware/authlimit.go`, around line 200). All other 429s
 * return a `Problem` with `code: plan_limit_concurrency` or
 * `code: quota_exhausted`.
 *
 */
export type RateLimitPlain = string;
