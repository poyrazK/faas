/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A bounded secret staging identity and complete private workload value sets.
 */
export type ProjectReleaseSecretStageRequest = {
  request_id: string;
  /**
   * Complete proposed workload secret snapshots within shared document and per-plan value limits. Values are accepted over TLS and sealed before storage.
   */
  workloads: Record<string, Record<string, string>>;
};

