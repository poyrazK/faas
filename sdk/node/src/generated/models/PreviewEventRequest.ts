/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Event fields to evaluate without persisting or delivering the event.
 */
export type PreviewEventRequest = {
  /**
   * Optional event id used when filters inspect the CloudEvents id.
   */
  id?: string;
  source: string;
  type: string;
  /**
   * Event occurrence time; omitted values use the current time.
   */
  time?: string;
  data_content_type?: 'application/json';
  /**
   * JSON event payload evaluated by subscription filters.
   */
  data: any;
};

