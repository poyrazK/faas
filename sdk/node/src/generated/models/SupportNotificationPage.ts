/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SupportNotification } from './SupportNotification.js';
/**
 * Sequence-ordered page of at most fifty updates in the authorized scope.
 */
export type SupportNotificationPage = {
  items: Array<SupportNotification>;
  next_cursor?: number;
  unread_count: number;
};

