/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppProtectionItemStatus } from './AppProtectionItemStatus.js';
/**
 * An app's production protection.
 */
export type AppProtectionResponse = {
  /**
   * on when the app is enrolled, off when its owner turned protection off, none when the app was never enrolled.
   */
  state: 'on' | 'off' | 'none';
  /**
   * True when the app is enrolled and every protection object is active.
   */
  protected: boolean;
  /**
   * Every protection object in a fixed order; empty when state is none.
   */
  items: Array<AppProtectionItemStatus>;
};

