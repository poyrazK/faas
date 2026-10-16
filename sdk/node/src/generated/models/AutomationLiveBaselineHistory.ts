/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveBaseline } from './AutomationLiveBaseline.js';
import type { AutomationLiveBaselineRetirement } from './AutomationLiveBaselineRetirement.js';
/**
 * Bounded promotion audit history and optional permanent retirement receipt for one baseline name.
 */
export type AutomationLiveBaselineHistory = {
  retirement?: AutomationLiveBaselineRetirement;
  promotions: Array<AutomationLiveBaseline>;
};

