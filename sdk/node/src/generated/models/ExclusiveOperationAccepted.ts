/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Receipt returned when an operation is durably accepted or joined.
 */
export type ExclusiveOperationAccepted = {
  id: string;
  /**
   * True only when an equivalent active operation was linked under join_existing.
   */
  joined: boolean;
  /**
   * Scoped status endpoint for the submitting credential.
   */
  status_url: string;
};

