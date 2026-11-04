/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An account's acknowledgement (no body) or note on an incident.
 */
export type IncidentNote = {
  id: string;
  kind: 'acknowledged' | 'note';
  body?: string;
  /**
   * An email or api_key:<id>.
   */
  author: string;
  created_at: string;
};

