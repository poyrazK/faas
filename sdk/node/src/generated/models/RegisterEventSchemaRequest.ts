/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable event schema registration request.
 */
export type RegisterEventSchemaRequest = {
  source: string;
  type: string;
  version: string;
  /**
   * Draft 2020-12 JSON Schema, at most 64 KiB, without external references.
   */
  schema: any;
};

