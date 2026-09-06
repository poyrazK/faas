/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectMultipartPart } from './ObjectMultipartPart.js';
/**
 * A page of provider-confirmed parts for an active upload.
 */
export type ObjectMultipartPartList = {
  items: Array<ObjectMultipartPart>;
  next_part_number_marker?: number;
};

