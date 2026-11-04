/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentSharedSecret } from './ProjectEnvironmentSharedSecret.js';
/**
 * A shared secret's metadata after a write.
 */
export type ProjectEnvironmentSharedSecretResponse = {
  project_slug: string;
  environment: string;
  secret: ProjectEnvironmentSharedSecret;
};

