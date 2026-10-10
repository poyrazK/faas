/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DurableEntityStateExport } from './DurableEntityStateExport.js';
/**
 * Private application-state export captured in a retained hourly backup slot.
 */
export type DurableEntityBackup = {
  /**
   * UTC hourly slot start.
   */
  captured_at: string;
  export: DurableEntityStateExport;
};

