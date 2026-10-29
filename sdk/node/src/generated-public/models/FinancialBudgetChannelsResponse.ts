/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { NotificationChannelResponse } from './NotificationChannelResponse.js';
/**
 * The notification channels a budget's threshold alerts also go to, with each channel's latest delivery outcome.
 */
export type FinancialBudgetChannelsResponse = {
  policy_id: string;
  channels: Array<NotificationChannelResponse>;
};

