/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An event an enrolled app published. It never reached the account's event bus; the sealed snapshot is its only record. Platform-generated IDs and times do not compare.
 */
export type ScenarioCapturedEvent = {
  id: string;
  workload: string;
  source: string;
  type: string;
  schema_version?: string;
  /**
   * Published JSON event data bounded to 64 KiB.
   */
  data: any;
};

