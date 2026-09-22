/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Edge-cache control primitive (ADR-122, kind=cache). Wraps the
 * response body in a per-route freshness window so a wake-elision
 * cache absorbs repeat traffic without paying a cold-boot cost.
 *
 * Field-by-field:
 * * `max_age_seconds` — required. Fresh window in seconds. The
 * apid-side default is 60; the absolute platform cap is 3600
 * (`api.ResponseCacheMaxAgeMaxSeconds`). The runtime cache
 * layer in `pkg/gateway/response_cache.go` re-checks this
 * cap as defence-in-depth.
 * * `stale_while_revalidate_seconds` — optional. After the fresh
 * window, return the cached response immediately while one
 * gateway request refreshes the key in the background. Default
 * 0 (disabled); absolute cap 300.
 * * `stale_if_error_seconds` — required. Stale-on-error window
 * in seconds. Default 300; absolute cap 300
 * (`api.ResponseCacheStaleIfErrorMaxSeconds`). During an
 * upstream error the gateway returns the cached body within
 * this window instead of 502/504.
 * * `vary_on` — optional closed vocabulary of non-credential
 * request headers that participate in the cache key.
 * Allowed values: `Accept-Language`, `Accept-Encoding`.
 * Credential-bearing headers (Authorization, Cookie) are
 * deliberately excluded — authed requests bypass the cache
 * entirely (ADR-122 D3).
 * * `methods` — optional closed vocabulary of HTTP methods
 * eligible for caching. Allowed values: `GET`, `HEAD`.
 * POST/PUT/PATCH/DELETE are deliberately excluded —
 * caching their responses is either incorrect (idempotency
 * breaks under retry) or unsafe (cross-user state).
 *
 */
export type EdgeRuleCacheAction = {
  /**
   * Fresh window in seconds. 0 = inherit the apid-side default
   * (60s). Positive values are clamped to the platform cap
   * (3600s = 1 hour).
   *
   */
  max_age_seconds: number;
  /**
   * After the fresh window, serve stale for this many seconds
   * while a single coalesced background request refreshes the
   * cache key. 0 disables stale-while-revalidate.
   *
   */
  stale_while_revalidate_seconds?: number;
  /**
   * Stale-on-error window in seconds. 0 = no stale-on-error
   * (errors return 502/504 directly). Positive values are
   * clamped to the platform cap (300s = 5 minutes).
   *
   */
  stale_if_error_seconds: number;
  /**
   * Non-credential request headers that participate in the
   * cache key. Closed vocabulary:
   * - `Accept-Language`
   * - `Accept-Encoding`
   * Empty array (default) collapses to no extra key
   * dimensions; the URL path + query alone drives cache
   * identity.
   *
   */
  vary_on?: Array<'Accept-Language' | 'Accept-Encoding'>;
  /**
   * HTTP methods eligible for caching. Closed vocabulary:
   * - `GET`
   * - `HEAD`
   * Empty array (default) collapses to the runtime's
   * cacheability predicate (GET + HEAD). POST/PUT/PATCH/DELETE
   * are deliberately absent.
   *
   */
  methods?: Array<'GET' | 'HEAD'>;
};

