/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable capacity wait receipt. Queue time does not consume the execution health window. An absent started_at means execution has never started.
 */
export type ProjectPromotionAdmission = {
  queued_at: string;
  wait_deadline: string;
  started_at?: string;
  reason: string;
};

