/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SupportMessage } from './SupportMessage.js';
/**
 * Sequence-ordered page of at most fifty public messages; use next_cursor as after.
 */
export type SupportMessagePage = {
  items: Array<SupportMessage>;
  next_cursor?: number;
};

