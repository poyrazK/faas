/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Idempotency key for the POST. Stored for 24h. On replay the server
 * returns the original response with `Idempotent-Replayed: true`.
 *
 */
export type IdempotencyKey = string;
