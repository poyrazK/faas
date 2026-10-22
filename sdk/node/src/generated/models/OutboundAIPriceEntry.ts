/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Price for model names starting with model_prefix ("*" matches any model; the longest prefix wins). Token prices are millicents per million tokens; absent cached, cache-write and input-image prices use the input price and an absent output-image price uses the output price. Media prices (ADR-997) are per image, per minute of input audio and per million input characters, and are zero when absent.
 */
export type OutboundAIPriceEntry = {
  model_prefix: string;
  input_millicents_per_mtok: number;
  cached_input_millicents_per_mtok?: number;
  cache_write_millicents_per_mtok?: number;
  output_millicents_per_mtok: number;
  input_image_millicents_per_mtok?: number;
  output_image_millicents_per_mtok?: number;
  /**
   * Charged per image when a call reports no output image tokens.
   */
  millicents_per_image?: number;
  /**
   * Charged per minute of transcribed audio.
   */
  millicents_per_audio_minute?: number;
  /**
   * Charged per million characters of speech input.
   */
  millicents_per_million_characters?: number;
  /**
   * Charged per second of generated video (ADR-1432). A model_prefix of <model>@<resolution>, such as veo-3.1-generate-001@4k, prices that resolution apart.
   */
  millicents_per_video_second?: number;
  /**
   * Charged per web search as the provider bills it: per call, query or grounded prompt (ADR-1433).
   */
  millicents_per_web_search?: number;
  /**
   * Charged per file search call (ADR-1433).
   */
  millicents_per_file_search?: number;
  /**
   * Charged per GB of code-interpreter container memory per session (ADR-1433).
   */
  millicents_per_code_session_gb?: number;
  /**
   * Charged per million uncached audio input tokens (ADR 20261019072722); the input price when absent.
   */
  input_audio_millicents_per_mtok?: number;
  /**
   * Charged per million cached audio input tokens; the cached input price when absent.
   */
  cached_input_audio_millicents_per_mtok?: number;
  /**
   * Charged per million audio output tokens; the output price when absent.
   */
  output_audio_millicents_per_mtok?: number;
};

