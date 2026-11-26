import { createHash, randomUUID } from "node:crypto";
import { createServer } from "node:http";

/** Application-owned access checks for both routes, including empty inboxes. */
export function createCsvWorkInboxServer({
  sdk,
  inbox,
  client,
  scope,
  realtime,
  authenticate,
  ownsImport,
}) {
  function target(actor, importId) {
    return sdk.workInboxWatchTarget({
      resource_id: createHash("sha256")
        .update(JSON.stringify([actor, importId]))
        .digest("hex"),
      business_ref: importId,
    });
  }
  const channel = ({ resource_id }) => "work-inbox:" + resource_id;
  const server = createServer(async (req, res) => {
    const send = (status, value) => {
      if (res.destroyed) return;
      res.writeHead(status, {
        "Content-Type": "application/json",
        "Cache-Control": "no-store",
      });
      res.end(JSON.stringify(value));
    };
    const route =
      /^\/imports\/([a-f0-9-]{36})\/work-inbox\/(ticket|snapshot)$/.exec(
        req.url,
      );
    if (req.method !== "GET" || !route) return send(404, {});
    const [, importId, action] = route;
    const actor = authenticate(req);
    if (!actor) return send(401, {});
    const abort = new AbortController();
    res.once("close", () => {
      if (!res.writableEnded) abort.abort();
    });
    const options = { timeoutMs: 10000, signal: abort.signal };
    try {
      if (!(await ownsImport(actor, importId))) return send(404, {});
      const bound = target(actor, importId);
      const value =
        action === "snapshot"
          ? await sdk.readWorkInboxSnapshot(inbox, bound, actor, options)
          : {
              ...(await client.realtime.issueRealtimeSubscriptionTicket(
                {
                  slug: scope.app_slug,
                  id: realtime.endpointId,
                  requestBody: {
                    principal: "inbox-" + actor,
                    channels: [channel(bound)],
                    ttl_seconds: 30,
                  },
                },
                { ...options, retry: false },
              )),
              target: bound,
              url: realtime.url,
              channel: channel(bound),
            };
      if (!(await ownsImport(actor, importId))) return send(404, {});
      send(200, value);
    } catch {
      send(503, {});
    }
  });

  return {
    server,
    async notify(actor, importId, changeId) {
      const bound = target(actor, importId);
      const hint = sdk.workInboxInvalidation(bound.resource_id, changeId);
      await client.realtime.publishManagedRealtimeChannel(
        {
          slug: scope.app_slug,
          id: realtime.endpointId,
          channel: channel(bound),
          delivery: "retained",
          idempotencyKey: "work-inbox:" + randomUUID(),
          requestBody: {
            data_base64: Buffer.from(JSON.stringify(hint)).toString("base64"),
            binary: false,
          },
        },
        { timeoutMs: 10000, retry: false },
      );
    },
  };
}
