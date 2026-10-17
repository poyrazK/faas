/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded object fixture input; raw content is never included in evidence responses.
 */
export type ScenarioObjectsData = {
  key: string;
  /**
   * Base64-encoded bytes, at most 65536 decoded bytes per object and 262144 decoded bytes in total.
   */
  body: string;
  content_type: string;
  metadata: Record<string, string>;
};

