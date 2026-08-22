/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Discriminator for an env-diff matrix row. 'secret' rows carry {present, value_hash}; 'env' rows carry {present, value}. The cell shape is uniform but the field population is kind-aware.
 */
export type EnvDiffKind = 'secret' | 'env';
