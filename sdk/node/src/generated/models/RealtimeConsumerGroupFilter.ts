/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable selection of retained events. Event types match metadata.event_type, not the created/updated/deleted lifecycle event. Nonempty event_types uses OR; every metadata predicate and the event-type condition must match. Empty selections match everything. Missing metadata never equals an expected empty value. Configuration is bounded to 8192 encoded JSON bytes.
 */
export type RealtimeConsumerGroupFilter = {
  event_types?: Array<string>;
  /**
   * Exact case-sensitive string equality; valid metadata keys use lowercase letters, digits, underscore, hyphen or dot and are 1 to 64 bytes. Values are at most 128 bytes and cannot contain NUL, CR or LF. Encoded metadata is bounded to 4096 bytes.
   */
  metadata?: Record<string, string>;
};

