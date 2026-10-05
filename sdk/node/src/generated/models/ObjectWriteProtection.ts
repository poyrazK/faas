/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectVersionLegalHold } from './ObjectVersionLegalHold.js';
import type { ObjectVersionRetention } from './ObjectVersionRetention.js';
/**
 * Fixed retention and independent legal hold for a new object version. Omitted retention inherits the admitted bucket default. Event holds and governance bypass are unsupported.
 */
export type ObjectWriteProtection = {
  retention?: ObjectVersionRetention;
  legal_hold?: ObjectVersionLegalHold;
};

