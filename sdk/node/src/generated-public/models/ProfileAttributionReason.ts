/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded evidence explaining why profile attribution is incomplete or inconsistent.
 */
export type ProfileAttributionReason = {
  reason: 'attributed' | 'unlabeled' | 'invalid_label' | 'route_not_admitted' | 'encoding_limit' | 'unknown';
  cpu_seconds: number;
};

