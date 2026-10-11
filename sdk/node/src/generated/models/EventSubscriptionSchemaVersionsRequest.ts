/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replacement schema-version selection for future subscription publications and backfills.
 */
export type EventSubscriptionSchemaVersionsRequest = {
  /**
   * Schema versions in the event subscription schema versions request: exact case-sensitive schema versions. Empty or omitted accepts all versions; a nonempty selection excludes unversioned events. Selection is captured at publication or backfill creation.
   */
  schema_versions: Array<string>;
};

