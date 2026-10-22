import { RealtimeService } from './generated/services/RealtimeService.js';

export interface RealtimePrincipalTarget {
  slug: string;
  id: string;
  principal: string;
}

/** Durable deny across enabled nodes; removes subscriptions after access refresh. */
export function revokeRealtimeChannelAccess(target: RealtimePrincipalTarget, channel: string) {
  return RealtimeService.changeManagedRealtimeAccess({ ...target, requestBody: { action: 'revoke_channel', channel } });
}

/** Remove the platform deny. The application still authorizes each subscription. */
export function restoreRealtimeChannelAccess(target: RealtimePrincipalTarget, channel: string) {
  return RealtimeService.changeManagedRealtimeAccess({ ...target, requestBody: { action: 'restore_channel', channel } });
}

/** Disconnect older sessions; reconnect requires a newly issued signed JWT. */
export function revokeRealtimeSessions(target: RealtimePrincipalTarget) {
  return RealtimeService.changeManagedRealtimeAccess({ ...target, requestBody: { action: 'revoke_sessions' } });
}
