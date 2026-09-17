/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppPrivateNetworkAttachment } from './AppPrivateNetworkAttachment.js';
/**
 * Capability metadata and the current private-network intent.
 */
export type AppPrivateNetworkAttachmentResponse = {
  feature_enabled: boolean;
  plan_allowed: boolean;
  max_cidrs: number;
  attachment: (AppPrivateNetworkAttachment | null);
};

