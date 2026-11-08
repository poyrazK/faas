// One slice of a stateless agent (ADR-20261030125501 §1). Each inbox
// message runs this once, and between messages the agent is only its rows
// in your Postgres: no process, VM or connection waits for a tool.
//
// A slice:
//   1. skips a message it already processed (delivery is at least once);
//   2. loads the thread's history;
//   3. asks the model for the next step;
//   4. persists the turn before it dispatches anything;
//   5. dispatches at most one asynchronous tool call, whose result comes
//      back as the thread's next inbox message;
//   6. returns.

// parseRecord reads a realtime trigger record. Your backend publishes user
// messages to agent-inbox with metadata thread_id, message_id and
// kind=user; Gregale publishes a tool's result with kind=tool_result.
export function parseRecord(record) {
  const md = record?.metadata ?? {};
  if (typeof record?.endpoint_id !== "string" || typeof md.thread_id !== "string" ||
      typeof md.message_id !== "string" || !["user", "tool_result"].includes(md.kind)) {
    return null;
  }
  let data;
  try {
    data = JSON.parse(Buffer.from(record.data_base64 ?? "", "base64").toString("utf8"));
  } catch {
    return null;
  }
  return { endpointId: record.endpoint_id, threadId: md.thread_id, messageId: md.message_id,
    role: md.kind === "user" ? "user" : "tool", content: data };
}

// runSlice handles one inbox message. store holds the thread (store.js);
// model picks the next step (model.js); platform issues and revokes callback
// URLs (platform.js); tools dispatches a tool call (tools.js).
export async function runSlice(message, { store, model, platform, tools }) {
  const turn = await store.transaction(message.threadId, async (tx) => {
    if (await tx.seen(message.messageId)) {
      // A redelivery: the turn is already persisted. Finish what the first
      // delivery may have stopped before: its tool call or its revocation.
      return { skipped: true, pending: await tx.undispatched(message.messageId), final: await tx.answered(message.messageId) };
    }
    const history = await tx.history();
    await tx.append(message.messageId, message.role, message.content);
    const step = model.next(history, message);
    if (step.call) {
      // Issued before commit: if issuing fails, nothing is persisted and the
      // consumer group retries the message.
      const callback = await platform.issueCallback(message.endpointId, message.threadId);
      const call = { tool: step.call.tool, input: step.call.input, callback_url: callback.url,
        expires_at: callback.expires_at, dispatched: false };
      await tx.append(assistantID(message.messageId), "assistant", { tool_call: call });
      return { pending: call };
    }
    const final = step.final === true;
    await tx.append(assistantID(message.messageId), "assistant", { text: step.text, final });
    return { final };
  });
  if (turn.pending) {
    await tools.dispatch(message.threadId, turn.pending);
    await store.markDispatched(message.threadId, assistantID(message.messageId));
  }
  if (turn.final) {
    // The thread is answered: every callback URL issued for it stops working.
    await platform.revokeCallbacks(message.endpointId, message.threadId);
  }
  return turn;
}

function assistantID(messageID) {
  return `assistant:${messageID}`;
}
