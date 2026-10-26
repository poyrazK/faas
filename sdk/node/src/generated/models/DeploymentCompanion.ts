/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One companion of a deployment: identity, image, port and size. Environment values and secret grants are never included.
 */
export type DeploymentCompanion = {
  /**
   * Companion name.
   */
  name: string;
  /**
   * Digest-pinned image reference.
   */
  image?: string;
  /**
   * Built-in preset the companion uses, when any.
   */
  preset?: string;
  /**
   * init or sidecar.
   */
  type?: string;
  /**
   * Declared port; it also opens the public <app>--<name> hostname.
   */
  port?: number;
  /**
   * Reserved RAM in MB.
   */
  ram_mb?: number;
  /**
   * Whether the companion receives the app's public traffic.
   */
  primary_ingress?: boolean;
};

