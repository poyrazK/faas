# agent-infinity-rust

A stateless agent built on [Infinity](https://github.com/hydro-project/infinity)'s
step mode, running as a Gregale function (ADR-20261030125501). Each message in a
conversation thread runs one `AgentSystem::step`. Between messages the function
parks, so a waiting agent costs 0 CPU and 0 RAM. It exists only as its rows in
your Postgres.

This starter is a preview behind the agents capability.

## How it works

1. Your backend publishes a user message to the `agent-inbox` channel of the
   app's `agent` realtime endpoint, with metadata `thread_id`, `message_id`
   and `kind=user`, and data `{"text": "..."}`.
2. The `slices` consumer group delivers the thread's messages one at a time,
   in order, and drops a repeated `message_id`. That is the per-thread
   serialization step mode needs. The realtime trigger runs the function
   for each message.
3. The slice runs one step with Infinity's stores on your Postgres
   (`src/store.rs`). Infinity loads the thread, runs at most one completion,
   saves history and dispatches at most one tool call over RAP.
4. The tool's RAP callback URL is issued for that thread just before the call
   goes out. The app asks Gregale for it with its own workload token, so it
   holds no API key.
5. The tool posts its RAP `tool_result` to that URL. The result becomes the
   thread's next inbox message and runs the next step, even if the function
   parked in between.
6. When the thread has answered and waits on nothing, the slice revokes the
   thread's callback URLs.

Infinity's built-in thread tools work too: a child thread's messages, reports
and tool errors go back through the inbox (`GregaleSender` in
`src/platform.rs`).

The model is scripted (`src/model.rs`), so the starter runs with no provider
key: a user message calls `lookup`, and the tool's result is the answer. To
use a real model, swap it for any Infinity provider, for example
`infinity-provider-rig`.

## Deploy

```sh
gregale deploy --template agent-infinity-rust --name <slug>
gregale postgres create <slug>-db
gregale postgres attach <slug>-db <slug> --env DATABASE_URL
gregale env set --app <slug> AGENT_READ_TOKEN=<a long random string> --restart
```

Publish a message from your backend, using an API key with `deploy:write`:

```sh
curl -X POST https://api.gregale.dev/v1/apps/<slug>/realtime/endpoints/<endpoint-id>/channels/agent-inbox/publish \
  -H "Authorization: Bearer $GREGALE_TOKEN" -H "content-type: application/json" \
  -d '{"data":"{\"text\":\"capital of Malta?\"}","metadata":{"thread_id":"t-1","message_id":"m-1","kind":"user"}}'
```

Without `TOOL_URL`, the `lookup` call is held, not sent. `GET /threads/t-1`
with `Authorization: Bearer $AGENT_READ_TOKEN` lists it under
`held_tool_calls`, with the fields a RAP tool server would receive. Post the
tool's result to its `callback_url` as RAP:

```sh
curl -X POST <callback_url> -H "content-type: application/json" \
  -d '{"type":"tool_result","group_id":"t-1","id":"<id>","text":"Valletta"}'
```

## Settings

| Setting | |
|---|---|
| `DATABASE_URL` | Infinity's stores: an attached Postgres database. The tables are created on first use. |
| `AGENT_READ_TOKEN` | Enables `GET /threads/:id` for this bearer token |
| `TOOL_URL` | Optional: a RAP tool server that `lookup` is invoked on |
| `GREGALE_API_URL` | The Gregale API; defaults to `https://api.gregale.dev` |

## Test

```sh
cargo test
INFINITY_TEST_DATABASE_URL=postgres://localhost/agent_test?sslmode=disable cargo test
```

The second command also runs the store contract and a two-step agent against
a real Postgres.
