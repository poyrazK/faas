/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current exact schema-version selection associated with one subscription.
 */
export type EventSubscriptionSchemaVersionsResponse = {
  subscription_id: string;
  /**
   * Schema versions in the event subscription schema versions response: exact case-sensitive schema versions. Empty or omitted accepts all versions; a nonempty selection excludes unversioned events. Selection is captured at publication or backfill creation.
   */
  schema_versions: Array<string>;
};

