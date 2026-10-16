/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationPlanStep } from './BindingVerificationPlanStep.js';
export type BindingVerificationRunStep = {
  plan: BindingVerificationPlanStep;
  resource_id?: string;
  resource_kind?: 'task' | 'queue';
  owned: boolean;
  status: string;
};

