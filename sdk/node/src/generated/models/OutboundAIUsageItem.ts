/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One UTC-day rollup for a caller and model. input_tokens includes cached reads and cache writes, which are also reported separately.
 */
export type OutboundAIUsageItem = {
  /**
   * App UUID, "runs" for stateless Runs, or "replay" for replayed calls (ADR-997).
   */
  caller: string;
  /**
   * Provider-reported model; "other" collects models beyond the daily distinct-model cap and "unknown" covers responses without one.
   */
  model: string;
  requests: number;
  error_requests: number;
  /**
   * Successful calls whose response reported no usage, such as an OpenAI stream without stream_options.include_usage.
   */
  usage_unknown_requests: number;
  input_tokens: number;
  cached_input_tokens: number;
  cache_write_tokens: number;
  output_tokens: number;
  /**
   * Priced from the list in effect at each call, else the built-in catalog, and rounded up per call; zero for an unpriced model.
   */
  cost_millicents?: number;
  /**
   * Images billed per image (ADR-997).
   */
  images: number;
  /**
   * Seconds of transcribed audio.
   */
  audio_seconds: number;
  /**
   * Characters of speech input.
   */
  characters: number;
  /**
   * Seconds of generated video (ADR-1432).
   */
  video_seconds: number;
  /**
   * Web searches as the provider bills them: per call, query or grounded prompt (ADR-1433).
   */
  web_searches: number;
  /**
   * File search calls (ADR-1433).
   */
  file_searches: number;
  /**
   * GB of code-interpreter container sessions (ADR-1433).
   */
  code_session_gb: number;
  /**
   * Requests the response cache served, with no tokens or cost (ADR-1440).
   */
  cached_requests: number;
  /**
   * Provider cost the response cache avoided, priced from the cached responses at the current price list.
   */
  cache_saved_millicents: number;
  /**
   * What provider prompt caching saved against plain input prices; negative when cache writes outweighed reads.
   */
  prompt_cache_saved_millicents: number;
  /**
   * Audio part of input_tokens, priced at the audio input price (ADR 20261019072722).
   */
  input_audio_tokens: number;
  /**
   * Audio part of output_tokens, priced at the audio output price.
   */
  output_audio_tokens: number;
};

