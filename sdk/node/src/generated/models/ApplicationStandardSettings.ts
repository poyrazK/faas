/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Logical control values; resource references contain UUIDs, never credentials.
 */
export type ApplicationStandardSettings = {
  log_destinations?: Array<string>;
  require_signed?: boolean;
  security_policy?: 'off' | 'warn' | 'enforce';
  trusted_publishers?: Array<string>;
  egress_cidrs?: Array<string>;
  egress_extra_ports?: Array<number>;
};

