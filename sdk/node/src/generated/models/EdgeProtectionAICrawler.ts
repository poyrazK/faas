/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One AI crawler's requests over the summary's range.
 */
export type EdgeProtectionAICrawler = {
  /**
   * Crawler name as the ai_crawler match field uses it, e.g. gptbot.
   */
  name: string;
  /**
   * Company that runs the crawler.
   */
  operator: string;
  /**
   * What its fetches are for: training models, AI search, or an assistant answering a user.
   */
  purpose: 'training' | 'search' | 'assistant';
  count: number;
};

