/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Candidate schema validation results for bounded retained event content.
 */
export type EventSchemaRolloutValidation = {
  /**
   * Zero-based index for caller-supplied samples.
   */
  sample_index?: number;
  /**
   * Identity for retained sample results.
   */
  event_id?: string;
  accepted_at?: string;
  valid: boolean;
  /**
   * Bounded validation category or unreadable_envelope; no payload values are returned.
   */
  reason?: string;
  /**
   * First failing field path, bounded to 256 UTF-8 bytes.
   */
  field?: string;
};

