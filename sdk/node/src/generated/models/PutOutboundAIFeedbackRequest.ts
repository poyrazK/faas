/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A rating of one logged call (ADR 20261021171712). A later rating replaces the earlier one.
 */
export type PutOutboundAIFeedbackRequest = {
  rating: 'good' | 'bad';
  label?: string;
};

