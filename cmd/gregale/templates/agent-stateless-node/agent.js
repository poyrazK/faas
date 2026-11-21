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
  const attempt = Number.isInteger(record.attempt) && record.attempt > 0 ? record.attempt : 1;
  return { endpointId: record.endpoint_id, threadId: md.thread_id, messageId: md.message_id,
    role: md.kind === "user" ? "user" : "tool", content: data, attempt };
}

// maxAttempts matches the slices group's max_attempts in gregale.yaml. A
// message that fails that many times is dead-lettered, and because the
// group's dead-letter policy is block, its thread then stops for good. So
// the last attempt does not fail: it closes the thread with a failed turn
// (failSlice) and completes, and every other thread is unaffected anyway.
export function maxAttempts(env = process.env) {
  const n = Number.parseInt(env.AGENT_MAX_ATTEMPTS ?? "", 10);
  return Number.isInteger(n) && n >= 1 && n <= 10 ? n : 5;
}

// handleSlice runs one slice. An error is rethrown while the consumer group
// will retry the message; on its last attempt the thread is closed with a
// failed turn instead, so one poisoned message cannot block its thread.
export async function handleSlice(message, deps, { attempts = maxAttempts(), log = console } = {}) {
  try {
    return await runSlice(message, deps);
  } catch (err) {
    if (message.attempt < attempts) throw err;
    return failSlice(message, err, deps, log);
  }
}

// failSlice records the failure as the thread's final turn, keeping the
// message that caused it, and revokes the thread's callback URLs. A store
// failure here still throws: the message is then dead-lettered and its
// thread waits for an operator.
export async function failSlice(message, err, { store, platform }, log = console) {
  const reason = String(err?.message ?? err).slice(0, 500);
  await store.transaction(message.threadId, async (tx) => {
    if (!(await tx.seen(message.messageId))) await tx.append(message.messageId, message.role, message.content);
    if (!(await tx.seen(failedID(message.messageId)))) {
      await tx.append(failedID(message.messageId), "assistant", { text: `This conversation stopped: ${reason}`, final: true, failed: true });
    }
  });
  try {
    await platform.revokeCallbacks(message.endpointId, message.threadId);
  } catch (revokeErr) {
    // The URLs still expire on their own; completing matters more.
    log.warn?.("revoking a failed thread's callback URLs failed", { thread_id: message.threadId, error: String(revokeErr?.message ?? revokeErr) });
  }
  return { failed: true, final: true, reason };
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
      const call = { id: assistantID(message.messageId), provider_call_id: step.call.provider_call_id ?? assistantID(message.messageId), tool: step.call.tool, input: step.call.input, callback_url: callback.url,
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

function failedID(messageID) {
  return `failed:${messageID}`;
}
