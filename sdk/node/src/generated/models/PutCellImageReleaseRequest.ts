/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CellImageReleaseOrdering } from './CellImageReleaseOrdering.js';
/**
 * Digest-pinned OCI image release using the existing regional app configuration.
 */
export type PutCellImageReleaseRequest = {
  ordering?: CellImageReleaseOrdering;
  image: string;
};

