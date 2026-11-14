/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolRegistryVersion } from './ToolRegistryVersion.js';
/**
 * Private immutable tool version summaries with an optional keyset continuation.
 */
export type ToolRegistryPage = {
  versions: Array<ToolRegistryVersion>;
  next_cursor?: string;
};

