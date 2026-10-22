/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The start of one captured body.
 */
export type OutboundAIContentBody = {
  /**
   * The captured bytes as text, or base64 when they are not UTF-8.
   */
  body: string;
  encoding: 'utf-8' | 'base64';
  /**
   * True when the body was longer than the 32 KiB kept, or streamed without buffering.
   */
  truncated: boolean;
  /**
   * The response Content-Type; null for requests.
   */
  content_type: string | null;
};

