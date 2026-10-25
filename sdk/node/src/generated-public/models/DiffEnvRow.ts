/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One would-write env var. Value carries plaintext so the
 * quota gate's per-value byte cap can fire (the wire's list
 * path never echoes values per ADR-053 §Decision 4).
 *
 */
export type DiffEnvRow = {
  key: string;
  value: string;
};

