/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One UTC-day rollup for a caller and model. input_tokens includes cached reads and cache writes, which are also reported separately.
 */
export type OutboundAIUsageItem = {
  /**
   * App UUID, "runs" for stateless Runs, or "replay" for replayed calls.
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
   * Images billed per image.
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
   * Seconds of generated video.
   */
  video_seconds: number;
  /**
   * Web searches as the provider bills them: per call, query or grounded prompt.
   */
  web_searches: number;
  /**
   * File search calls.
   */
  file_searches: number;
  /**
   * GB of code-interpreter container sessions.
   */
  code_session_gb: number;
  /**
   * Requests the response cache served, with no tokens or cost.
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
   * Audio part of input_tokens, priced at the audio input price.
   */
  input_audio_tokens: number;
  /**
   * Audio part of output_tokens, priced at the audio output price.
   */
  output_audio_tokens: number;
  /**
   * Answered calls that stopped at the output-token limit.
   */
  truncated_requests: number;
  /**
   * Answered calls the provider refused or filtered.
   */
  refused_requests: number;
  /**
   * Answered calls with no text and no requested tools.
   */
  empty_requests: number;
  /**
   * Answered calls whose requested JSON, or tool-call arguments, did not parse.
   */
  invalid_json_requests: number;
  /**
   * Calls whose newest turn reported a failed tool.
   */
  tool_error_requests: number;
  /**
   * Calls rated good.
   */
  good_feedback: number;
  /**
   * Calls rated bad.
   */
  bad_feedback: number;
  /**
   * Calls an online evaluation scored.
   */
  evaluated_requests: number;
  /**
   * Sum of their scores from 1 to 5; divide by evaluated_requests for the mean.
   */
  evaluation_score_sum: number;
  /**
   * Sampled calls the judge could not score.
   */
  evaluation_errors: number;
};

