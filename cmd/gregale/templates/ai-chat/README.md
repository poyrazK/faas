# ai-chat

A minimal port-8080 Node.js endpoint that proxies chat completions
to either **OpenAI** or **Anthropic**. Pick a provider by setting
exactly one of `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`. The model
defaults to `gpt-4o-mini` (OpenAI) or `claude-3-5-sonnet-latest`
(Anthropic); both are overridable via env.

This is a SCAFFOLD, not a production chat app — it exposes a single
`POST /chat` endpoint that takes a `messages` array and returns the
provider's reply plus token usage. Streaming, conversation history
persistence, and tool-use are out of scope (the customer adds them
on top).

## First deploy with secrets

Create the file outside this directory, restrict it to your user, and
deploy. Set exactly one provider key. Gregale creates the app, seals the
secrets, and only then starts the runtime:

```sh
cat > ../ai-chat.secrets <<'EOF'
OPENAI_API_KEY=sk-...
# Or use ANTHROPIC_API_KEY=sk-ant-..., but never both.
# Optional: OPENAI_MODEL=gpt-4o and SYSTEM_PROMPT=...
EOF
chmod 600 ../ai-chat.secrets
gregale deploy --secrets-file ../ai-chat.secrets
```

## Rotate or update secrets

After the app exists, use `gregale secrets set --app <slug> KEY=VALUE`
to change the provider or optional settings, then redeploy.

## Pick a provider

```sh
# OpenAI
gregale secrets set --app <slug> OPENAI_API_KEY=sk-...

# Anthropic
gregale secrets set --app <slug> ANTHROPIC_API_KEY=sk-ant-...
```

Setting both is rejected at startup with an actionable error.
Setting neither is rejected too — pick one.

## Optional knobs

```sh
# Pick a specific model (defaults are sensible)
gregale secrets set --app <slug> OPENAI_MODEL=gpt-4o
gregale secrets set --app <slug> ANTHROPIC_MODEL=claude-3-opus-20240229

# Prepend a system prompt to every conversation. Keep it short —
# every byte costs tokens on every request.
gregale secrets set --app <slug> SYSTEM_PROMPT='You are a helpful assistant for a coffee shop. Be concise.'
```

## Deploy

From this directory:

```sh
gregale deploy
```

## Try it

```sh
# Health probe shows which provider is wired up
curl https://<slug>.gregale.dev/healthz
# → {"ok":true,"provider":"openai","model":"gpt-4o-mini"}

# A single-turn chat
curl -X POST -H 'content-type: application/json' \
     -d '{"messages":[{"role":"user","content":"Hello, who are you?"}]}' \
     https://<slug>.gregale.dev/chat

# A multi-turn chat
curl -X POST -H 'content-type: application/json' \
     -d '{
       "messages":[
         {"role":"user","content":"What is 2+2?"},
         {"role":"assistant","content":"4"},
         {"role":"user","content":"And 4+4?"}
       ]
     }' \
     https://<slug>.gregale.dev/chat
```

## Response shape

```json
{
  "ok": true,
  "provider": "openai",
  "model": "gpt-4o-mini",
  "reply": "Hello! I'm a helpful assistant.",
  "usage": { "input_tokens": 14, "output_tokens": 7 }
}
```

`usage` is forwarded from the upstream provider so you can build a
per-request cost meter on top.

## Switching providers mid-flight

Re-deploy after updating the secret. The provider is read at
process startup so an existing wake won't switch until the next
cold boot — the cold-boot cost is what makes this safe (no
half-migrated state).

## Re-deploy after edits

Edit `handler.js`, then `gregale deploy` from this directory.
