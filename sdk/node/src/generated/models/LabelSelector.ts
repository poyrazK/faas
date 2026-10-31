/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Preview (resource-labels capability). A Kubernetes label selector, such as team=billing,tier in (web,api),!canary; only matching resources are listed (domains: those of matching apps). Supports =, ==, !=, key, !key, in and notin; at most 4096 bytes. An invalid selector is a 400 invalid_label_selector; a selector while the capability is off is a 403 capability_not_released.
 */
export type LabelSelector = string;
