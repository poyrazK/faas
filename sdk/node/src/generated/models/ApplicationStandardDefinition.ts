/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardCIDRRule } from './ApplicationStandardCIDRRule.js';
import type { ApplicationStandardExtraPortRule } from './ApplicationStandardExtraPortRule.js';
import type { ApplicationStandardLogDestinationRule } from './ApplicationStandardLogDestinationRule.js';
import type { ApplicationStandardPublisherRule } from './ApplicationStandardPublisherRule.js';
import type { ApplicationStandardSecurityPolicyRule } from './ApplicationStandardSecurityPolicyRule.js';
import type { ApplicationStandardSignatureRule } from './ApplicationStandardSignatureRule.js';
/**
 * Supported versioned application requirements. Resource UUIDs must belong to the organization.
 * Enforced destination, publisher and CIDR sets cannot be empty.
 * Empty local CIDRs mean unrestricted access and cannot satisfy a restriction.
 *
 */
export type ApplicationStandardDefinition = {
  log_destinations?: ApplicationStandardLogDestinationRule;
  require_signed?: ApplicationStandardSignatureRule;
  security_policy?: ApplicationStandardSecurityPolicyRule;
  trusted_publishers?: ApplicationStandardPublisherRule;
  egress_cidrs?: ApplicationStandardCIDRRule;
  egress_extra_ports?: ApplicationStandardExtraPortRule;
};

