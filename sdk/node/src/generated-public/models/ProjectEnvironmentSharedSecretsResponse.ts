/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentSharedSecret } from './ProjectEnvironmentSharedSecret.js';
/**
 * The secrets a project environment shares with its services.
 */
export type ProjectEnvironmentSharedSecretsResponse = {
  project_slug: string;
  environment: string;
  secrets: Array<ProjectEnvironmentSharedSecret>;
  /**
   * The plan's ProjectSecretCountMax for one environment.
   */
  limit: number;
};

