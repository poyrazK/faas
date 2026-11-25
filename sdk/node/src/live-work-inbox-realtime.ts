import {
  consumeRealtimeChannels,
  type RealtimeConnectionOptions,
} from "./realtime-resume.js";
import type { LiveWorkInboxSource } from "./live-work-inbox.js";

/** Bind to one private channel. Fetch a fresh authorized ticket on every connect. */
export function realtimeLiveWorkInboxSource(
  input: Omit<
    RealtimeConnectionOptions,
    "signal" | "inbox" | "onDirectMessage"
  > & { channel: string },
): LiveWorkInboxSource {
  const { channel, ...options } = input;
  return (sink, signal) => {
    let cursor = 0;
    const controller = new AbortController(),
      linked = AbortSignal.any([signal, controller.signal]);
    void consumeRealtimeChannels({
      ...options,
      signal: linked,
      async webSocketFactory(url, protocols) {
        try {
          return await options.webSocketFactory(url, protocols);
        } catch (error) {
          if (
            error &&
            typeof error === "object" &&
            "code" in error &&
            error.code === "unauthorized"
          )
            sink.failed("unauthorized");
          throw error;
        }
      },
      onConnectionChange(state) {
        if (state === "disconnected") sink.disconnected();
        options.onConnectionChange?.(state);
      },
      channels: [
        {
          channel,
          cursorStore: {
            load: () => cursor,
            save: (next) => {
              cursor = next;
            },
          },
          onSubscribed() {
            sink.ready();
          },
          onResync(error) {
            sink.disconnected();
            return error.latestSequence;
          },
          onSubscriptionError(code) {
            sink.failed(
              code === "access_revoked" || code === "not_authorized"
                ? "unauthorized"
                : "subscription_failed",
            );
          },
          onMessage(message) {
            if (
              message.binary ||
              message.deleted ||
              message.data.byteLength > 4096
            )
              return;
            try {
              sink.changed(
                JSON.parse(
                  new TextDecoder("utf-8", { fatal: true }).decode(
                    message.data,
                  ),
                ),
              );
            } catch {
              /* unrelated or malformed notification */
            }
          },
        },
      ],
    }).catch(() => {
      if (!linked.aborted) sink.failed();
    });
    return () => controller.abort();
  };
}
