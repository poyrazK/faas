/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Live retained instance-history page. App/window filtering precedes the bound; missing start times are excluded. Counts describe this page only. Source success does not prove capture completeness, freshness or a snapshot. Instance IDs stay in opaque cursors.
 */
export type AppWakeHistoryPage = {
  mode: 'keyset_v1';
  since: string;
  until: string;
  limit: number;
  cursor?: string;
  next_cursor?: string;
  complete: boolean;
  source_status: 'read_succeeded';
  count_scope: 'returned_page';
};

