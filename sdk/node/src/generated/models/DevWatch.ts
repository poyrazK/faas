/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Watch mode (ADR-1170). The environment runs this development server instead of the production build, and live source patches reach the server's own reloader. Omit to turn watch mode off; the change applies from the next developer build. Refused with 422 dev_watch_unsupported when the developer app has less than 512 MB of RAM or does not require authentication.
 */
export type DevWatch = {
  /**
   * One-line command that starts the development server, for example "npm run dev".
   */
  command: string;
};

