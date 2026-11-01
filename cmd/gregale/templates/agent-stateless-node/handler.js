// agent-stateless-node — a stateless agent on a Gregale function
// (ADR-20261030125501). The realtime trigger runs this handler once per
// agent-inbox message; between messages the function parks and holds no
// RAM, and the conversation is only its rows in your Postgres.
//
// Routes:
//   trigger record (POST)      one slice of the thread (agent.js)
//   GET /threads/:id           the thread, for your backend; needs
//                              Authorization: Bearer $AGENT_READ_TOKEN
//   POST /realtime/connect     refused: the inbox takes messages from your
//                              backend, never from browser clients
//
// Settings: DATABASE_URL (attach a Postgres database), AGENT_READ_TOKEN
// (optional; GET /threads is off without it), TOOL_URL (optional, tools.js),
// GREGALE_API_URL (defaults to https://api.gregale.dev).
import { timingSafeEqual } from "node:crypto";
import { parseRecord, runSlice } from "./agent.js";
import { scriptedModel } from "./model.js";
import { GregalePlatform } from "./platform.js";
import { PostgresStore } from "./store.js";
import { HTTPTools } from "./tools.js";

let deps;

function dependencies() {
  deps ??= { store: new PostgresStore(), model: scriptedModel, platform: new GregalePlatform(), tools: new HTTPTools() };
  return deps;
}

function json(statusCode, value) {
  return { statusCode, headers: { "content-type": "application/json" }, body: JSON.stringify(value) };
}

function parseBody(event) {
  const body = event?.body;
  if (body && typeof body === "object") return body;
  try {
    return JSON.parse(body ?? "");
  } catch {
    return null;
  }
}

function authorized(event) {
  const want = process.env.AGENT_READ_TOKEN ?? "";
  const got = String(event?.headers?.authorization ?? event?.headers?.Authorization ?? "").replace(/^Bearer /, "");
  return want.length > 0 && got.length === want.length && timingSafeEqual(Buffer.from(got), Buffer.from(want));
}

export async function handler(event, ctx) {
  const method = String(event?.method ?? "POST").toUpperCase();
  const path = String(event?.path ?? "/").split("?")[0];
  if (path.startsWith("/realtime/")) {
    return path === "/realtime/connect" ? json(403, { error: "the agent inbox takes no client connections" }) : json(200, {});
  }
  const thread = /^\/threads\/([A-Za-z0-9._:-]{1,64})$/.exec(path);
  if (method === "GET" && thread) {
    if (!authorized(event)) return json(404, { error: "not found" });
    return json(200, { thread_id: thread[1], messages: await dependencies().store.thread(thread[1]) });
  }
  const message = parseRecord(parseBody(event));
  if (!message) {
    // Not an inbox message: complete it, since a retry cannot fix it.
    ctx.log.warn("ignored a record that is not an agent-inbox message");
    return json(200, { ignored: true });
  }
  const turn = await runSlice(message, dependencies());
  ctx.log.info("slice", { thread_id: message.threadId, message_id: message.messageId,
    skipped: turn.skipped === true, tool_call: Boolean(turn.pending), final: turn.final === true });
  return json(200, { ok: true });
}
