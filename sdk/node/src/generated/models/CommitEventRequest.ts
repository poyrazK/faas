/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * At-least-once handoff. Repeating the identity with identical JSON data returns the original receipt; changed type or data conflicts.
 */
export type CommitEventRequest = {
  id: string;
  type: string;
  data: any;
};

