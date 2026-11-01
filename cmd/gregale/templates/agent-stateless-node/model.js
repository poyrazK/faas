// The model step. Replace scriptedModel with your loop: call a model through
// your Gregale AI integration (see the ai-chat template), or run Infinity's
// step mode. A step returns either { call: { tool, input } } to dispatch one
// asynchronous tool call, or { text, final } to answer.
//
// The scripted model needs no provider key, so the starter runs end to end
// as deployed: a user message asks the lookup tool, and the tool's result
// is the answer.
export const scriptedModel = {
  next(history, message) {
    if (message.role === "user") {
      return { call: { tool: "lookup", input: { query: String(message.content?.text ?? "") } } };
    }
    return { text: `lookup answered: ${JSON.stringify(message.content)}`, final: true };
  },
};
