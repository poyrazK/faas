/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One app-owned public datagram listener with a reserved stable port.
 */
export type UDPListenerResponse = {
  id: string;
  name: string;
  guest_port: number;
  public_port: number;
  protocol: 'udp';
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

