/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectVersionLegalHold } from './ObjectVersionLegalHold.js';
import type { ObjectVersionRetention } from './ObjectVersionRetention.js';
/**
 * Fixed or enrolled event retention and an independent legal hold for a new object version. Omitted retention inherits the immutable admitted bucket default. Event hold ON requires one days or years duration and permits an optional minimum date. Event hold OFF on creation requires an explicit fixed date and no duration. Governance bypass is unsupported.
 */
export type ObjectWriteProtection = {
  retention?: ObjectVersionRetention;
  legal_hold?: ObjectVersionLegalHold;
};

