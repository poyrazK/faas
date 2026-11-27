/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A killed flag and the configuration version that holds the kill.
 */
export type FeatureFlagKillResponse = {
  key: string;
  /**
   * Always true.
   */
  killed: boolean;
  /**
   * False when the flag was already killed and no configuration was published.
   */
  changed: boolean;
  version: number;
};

