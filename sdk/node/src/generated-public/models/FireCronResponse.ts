/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The 202 body for `POST /v1/crons/{id}/run`.
 * `request_id` is the durable handle on the fire-now row in
 * `cron_fire_now_requests`; poll
 * `GET /v1/crons/{id}/runs` for the matching `cron.fired.manually`
 * audit row, or watch the future `GET /v1/cron-fire-now-requests/{id}`
 * for terminal status. `status` is `"pending"` at the moment of
 * the response; transitions to `"succeeded"` or `"failed"`
 * asynchronously as the scheduler claims the row.
 *
 */
export type FireCronResponse = {
  request_id: string;
  cron_id: string;
  status: 'pending' | 'running' | 'succeeded' | 'failed';
};

