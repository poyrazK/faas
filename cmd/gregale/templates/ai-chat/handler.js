// ai-chat — Move 1 stateless-contract template.
//
// A SCAFFOLD that exposes a single POST /chat endpoint that proxies
// a prompt to either OpenAI's or Anthropic's chat-completions API.
// The customer picks a provider by setting one of OPENAI_API_KEY or
// ANTHROPIC_API_KEY (not both). The model defaults to gpt-4o-mini
// for OpenAI and claude-opus-5 for Anthropic, both
// overridable via env.
//
// Required env vars (set via `gregale secrets set --app <slug> ...`):
//
//	OPENAI_API_KEY     — OpenAI secret. Picks OpenAI provider.
//	ANTHROPIC_API_KEY  — Anthropic secret. Picks Anthropic provider.
//	                   Set exactly ONE. Setting both is rejected at
//	                   startup with an actionable hint.
//	OPENAI_MODEL       — optional, defaults to "gpt-4o-mini"
//	ANTHROPIC_MODEL    — optional, defaults to "claude-opus-5"
//	SYSTEM_PROMPT      — optional, prefixed to every conversation.
//	                   Keep it short — every byte costs tokens.
//
// Optional request body shape:
//
//	{
//	  "messages": [
//	    { "role": "user", "content": "Hello!" }
//	  ],
//	  "model": "gpt-4o-mini"        // optional per-request override
//	}
//
// Response body shape (unified across providers):
//
//	{
//	  "ok": true,
//	  "provider": "openai" | "anthropic",
//	  "model": "<resolved-model>",
//	  "reply": "<assistant text>",
//	  "usage": { "input_tokens": N, "output_tokens": M }
//	}

import express from "express";

const OPENAI_KEY = process.env.OPENAI_API_KEY || "";
const ANTHROPIC_KEY = process.env.ANTHROPIC_API_KEY || "";
const OPENAI_MODEL = process.env.OPENAI_MODEL || "gpt-4o-mini";
const ANTHROPIC_MODEL = process.env.ANTHROPIC_MODEL || "claude-opus-5";

// Models that accept server-side refusal fallbacks (`fallbacks: "default"`):
// a declined request is re-served by Anthropic's recommended fallback model
// in the same call instead of coming back empty. Other models reject the
// parameter, so it is only sent for these.
const ANTHROPIC_FALLBACK_MODELS = /^claude-(opus-5|fable-5-1)$/;
const SYSTEM_PROMPT = process.env.SYSTEM_PROMPT || "";

function provider() {
  if (OPENAI_KEY && ANTHROPIC_KEY) {
    throw new Error("both OPENAI_API_KEY and ANTHROPIC_API_KEY are set; pick one");
  }
  if (OPENAI_KEY) {
    return "openai";
  }
  if (ANTHROPIC_KEY) {
    return "anthropic";
  }
  throw new Error(
    "no provider key set; run `gregale secrets set --app <slug> OPENAI_API_KEY=...` or ANTHROPIC_API_KEY=...",
  );
}

let activeProvider;
try {
  activeProvider = provider();
} catch (err) {
  console.error(err.message);
  process.exit(1);
}

function defaultModel() {
  return activeProvider === "openai" ? OPENAI_MODEL : ANTHROPIC_MODEL;
}

const app = express();
const port = process.env.PORT || 8080;
app.use(express.json({ limit: "64kb" }));

async function callOpenAI(messages, model) {
  const body = {
    model,
    messages: SYSTEM_PROMPT
      ? [{ role: "system", content: SYSTEM_PROMPT }, ...messages]
      : messages,
  };
  const r = await fetch("https://api.openai.com/v1/chat/completions", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${OPENAI_KEY}`,
    },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const text = await r.text();
    throw new Error(`openai ${r.status}: ${text.slice(0, 512)}`);
  }
  const j = await r.json();
  return {
    reply: j.choices?.[0]?.message?.content || "",
    usage: {
      input_tokens: j.usage?.prompt_tokens || 0,
      output_tokens: j.usage?.completion_tokens || 0,
    },
  };
}

async function callAnthropic(messages, model) {
  // Anthropic's API takes system as a top-level field, not as a
  // message. The SDKs handle that conversion; we replicate it here
  // so we don't pull in @anthropic-ai/sdk for one endpoint.
  const systemParts = [];
  const chatMessages = [];
  for (const m of messages) {
    if (m.role === "system") {
      systemParts.push(m.content);
    } else {
      chatMessages.push(m);
    }
  }
  if (SYSTEM_PROMPT) {
    systemParts.unshift(SYSTEM_PROMPT);
  }
  // Current Claude models think adaptively by default, and thinking shares
  // max_tokens with the reply: a small cap can leave no room for text.
  const body = {
    model,
    max_tokens: 16000,
    system: systemParts.join("\n\n") || undefined,
    messages: chatMessages,
  };
  const headers = {
    "content-type": "application/json",
    "x-api-key": ANTHROPIC_KEY,
    "anthropic-version": "2023-06-01",
  };
  if (ANTHROPIC_FALLBACK_MODELS.test(model)) {
    body.fallbacks = "default";
    headers["anthropic-beta"] = "server-side-fallback-2026-07-01";
  }
  const r = await fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const text = await r.text();
    throw new Error(`anthropic ${r.status}: ${text.slice(0, 512)}`);
  }
  const j = await r.json();
  if (j.stop_reason === "refusal") {
    // A declined request (after any fallback) carries no usable content.
    throw new Error(`anthropic refused the request (${j.stop_details?.category || "unspecified"})`);
  }
  const reply =
    (j.content || [])
      .filter((c) => c.type === "text")
      .map((c) => c.text)
      .join("") || "";
  return {
    reply,
    usage: {
      input_tokens: j.usage?.input_tokens || 0,
      output_tokens: j.usage?.output_tokens || 0,
    },
  };
}

app.post("/chat", async (req, res) => {
  const messages = Array.isArray(req.body?.messages) ? req.body.messages : null;
  if (!messages || messages.length === 0) {
    return res.status(400).json({ ok: false, error: "missing 'messages' array" });
  }
  const model = req.body?.model || defaultModel();
  try {
    const out =
      activeProvider === "openai"
        ? await callOpenAI(messages, model)
        : await callAnthropic(messages, model);
    return res.status(200).json({
      ok: true,
      provider: activeProvider,
      model,
      reply: out.reply,
      usage: out.usage,
    });
  } catch (err) {
    console.error("chat failed:", err.message);
    return res.status(502).json({ ok: false, error: err.message });
  }
});

app.get("/healthz", (_req, res) => {
  res.status(200).json({
    ok: true,
    provider: activeProvider,
    model: defaultModel(),
  });
});

app.listen(port, () => {
  console.log(`ai-chat listening on :${port} (provider=${activeProvider} model=${defaultModel()})`);
});
