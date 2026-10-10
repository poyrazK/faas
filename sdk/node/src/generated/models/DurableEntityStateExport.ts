/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DurableEntityScope } from './DurableEntityScope.js';
/**
 * Checksummed committed application data with immutable entity identity; excludes recovery and delivery history.
 */
export type DurableEntityStateExport = {
  format: 1;
  entity: DurableEntityScope;
  version: number;
  /**
   * Opaque application JSON, including any application schema envelope.
   */
  data: any;
  checksum: string;
};

