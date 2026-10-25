/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Org slug. Lowercase letters, digits, hyphens; must start
 * and end with alnum. 3..32 chars. Mirrors `OrgSlugPattern`
 * in `pkg/api/errors.go` exactly so the spec drift gate
 * (`make spec-check`) stays green.
 *
 */
export type OrgSlug = string;
