/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Env-var scope (ADR-090). A domain-valid slug (1..40 chars,
 * lowercase alnum + dash, no leading/trailing dash) — e.g.
 * `default`, `staging`, `prod-eu`. Or the reserved sentinel
 * `__all__` on GET only, which returns the nested
 * `env_by_scope` response shape (every scope on the app).
 * Omitted = `scope=default` (pre-PR-B behavior).
 *
 */
export type EnvScope = string;
