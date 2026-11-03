/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Compose process overrides for a prebuilt image. Omitted fields inherit the image's ENTRYPOINT, USER, WORKDIR and stop settings.
 */
export type ComposeProcess = {
  /**
   * Replaces the image ENTRYPOINT and drops its CMD; the Compose command supplies the arguments. An empty array clears the ENTRYPOINT.
   */
  entrypoint?: Array<string>;
  /**
   * User name or id, optionally with :group, resolved against the image's /etc/passwd and /etc/group.
   */
  user?: string;
  /**
   * Clean absolute working directory.
   */
  working_dir?: string;
  stop_signal?: 'SIGTERM' | 'SIGINT' | 'SIGQUIT' | 'SIGHUP' | 'SIGUSR1' | 'SIGUSR2';
  /**
   * Seconds between the stop signal and SIGKILL, capped by the plan.
   */
  stop_grace_period_s?: number;
};

