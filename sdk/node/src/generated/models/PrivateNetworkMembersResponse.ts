/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PrivateNetworkMember } from './PrivateNetworkMember.js';
/**
 * Account-scoped member inventory and address capacity for one network.
 */
export type PrivateNetworkMembersResponse = {
  network_id: string;
  cidr: string;
  /**
   * Allocatable member addresses after network, gateway, and broadcast reservations.
   */
  capacity: number;
  used: number;
  available: number;
  members: Array<PrivateNetworkMember>;
};

