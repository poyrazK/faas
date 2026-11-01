# agent-stateless-node

A stateless agent on a Gregale function (ADR-20261030125501). Each message
in a conversation thread runs the function once. Between messages the
function parks, so a waiting agent costs 0 CPU and 0 RAM: it is only its
rows in your Postgres.

This starter is a preview behind the agents capability.

## How it works

1. Your backend publishes a user message to the `agent-inbox` channel of the
   app's `agent` realtime endpoint, with metadata `thread_id`, `message_id`
   and `kind=user`.
2. The `slices` consumer group delivers the thread's messages one at a time,
   in order, and drops a repeated `message_id`. The realtime trigger runs the
   function for each one.
3. A slice loads the thread from Postgres, asks the model for the next step,
   persists the turn, and dispatches at most one tool call. A tool call gets
   a callback URL for its thread. The app asks Gregale for it with its own
   workload token, so it holds no API key.
4. The tool POSTs its result to the callback URL. That result becomes the
   thread's next inbox message and runs the next slice. The function wakes
   for it, even after parking.
5. When the agent answers, it revokes the thread's callback URLs.

The model here is scripted (`model.js`), so the starter runs with no
provider key: a user message calls the `lookup` tool, and the tool's
result is the answer. Replace it with your loop, for example a model
called through your Gregale AI integration (see the `ai-chat` template).

## Deploy

```sh
gregale deploy --template agent-stateless-node --name <slug>
gregale postgres create <slug>-db
gregale postgres attach <slug>-db <slug> --env DATABASE_URL
gregale env set --app <slug> AGENT_READ_TOKEN=<a long random string> --restart
```

Publish a message from your backend (an API key with `deploy:write`):

```sh
curl -X POST https://api.gregale.dev/v1/apps/<slug>/realtime/endpoints/<endpoint-id>/channels/agent-inbox/publish \
  -H "Authorization: Bearer $GREGALE_TOKEN" -H "content-type: application/json" \
  -d '{"data":"{\"text\":\"capital of Malta?\"}","metadata":{"thread_id":"t-1","message_id":"m-1","kind":"user"}}'
```

Read the thread with `GET /threads/t-1` and `Authorization: Bearer
$AGENT_READ_TOKEN`. A pending tool call shows its `callback_url`. Post the
tool's result there:

```sh
curl -X POST <callback_url> -H "content-type: application/json" \
  -d '{"message_id":"r-1","kind":"tool_result","data":{"answer":"Valletta"}}'
```

## Settings

| Setting | |
|---|---|
| `DATABASE_URL` | The thread store: an attached Postgres database |
| `AGENT_READ_TOKEN` | Enables `GET /threads/:id` for this bearer token |
| `TOOL_URL` | Optional: each tool call is POSTed here with its callback URL |
| `GREGALE_API_URL` | The Gregale API; defaults to `https://api.gregale.dev` |

## Test

```sh
npm test
```
