/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppErrorRequestItem } from './AppErrorRequestItem.js';
/**
 * Drill-down page returned by `GET /v1/apps/{slug}/errors/{fingerprint}`. The header fields (fingerprint,
 * error_class, route, http_status) are denormalised from the
 * parent summary row so the dashboard renders the page header
 * without a second round-trip. Does NOT include
 * `headers_sample` or `redactions` — those are on the
 * `/first` endpoint only.
 *
 */
export type AppErrorRequestsResponse = {
  fingerprint: string;
  error_class: string;
  route: string;
  http_status: number;
  requests: Array<AppErrorRequestItem>;
  next_cursor?: string | null;
};

