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

A slice that throws is retried. The inbox's dead-letter policy is `block`,
so a message that used up its attempts would stop its thread for good. The
last attempt (`AGENT_MAX_ATTEMPTS`, equal to the group's `max_attempts`)
therefore closes the thread with a failed final turn
(`{"final": true, "failed": true}`) and completes; `GET /threads/:id` shows
why. Other threads are never affected.

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
| `AGENT_MAX_ATTEMPTS` | Attempts before a failing message closes its thread; default 5. Keep it equal to `max_attempts` in `gregale.yaml` |
| `GREGALE_API_URL` | The Gregale API; defaults to `https://api.gregale.dev` |

## Test

```sh
npm test
```

## Run task pipelines

Use `GregalePipelineTools({ platform })` from `tools.js` to dispatch bounded
version-pinned task graphs or batches through RAP `gregale_pipeline`. Publish each
task with this app's endpoint in `agent_endpoint_ids`. Tool input contains `steps`
or `batch` and optional pipeline limits; the platform owns workflow identity.
Persist the call ID before dispatch, as this starter already does for tool calls.

The terminal inbox result provides `completion_id`, `workflow_id`, `outcome` and a
scoped manifest reference. `platform.pipelineReceipt(completion_id)` reads the
complete metadata manifest; use `taskReceipt(runID)` and `taskArtifact(runID,name)`
for referenced outputs. Existing Run output retention applies. Revoking the
original thread grant also closes these pipeline reads. See
[Run pipelines](../../../../docs/run-pipelines.md) for the preview contract.
## Durable report outputs

`platform.deliverRunArtifact(app, route, request)` promotes a declared successful
Run output into a configured app-owned upload route. Its explicit delivery key
deduplicates storage writes independently of execution. Wait for `completed`
before persisting the final turn and revoking thread callbacks. Persist only the
delivery receipt/object reference in the conversation. The report remains under
app storage policy after the temporary source grant is gone.

`platform.artifactDelivery(app, route, id)` reads its durable status, and
`platform.signArtifactDelivery(app, route, id)` issues a short-lived download URL
under app ownership. Authenticate the customer and check their report ownership
before issuing a URL; keep the URL out of model history. See the
[delivery guide](../../../../docs/run-artifact-delivery.md) and
[report Agent example](../../../../examples/gregale-run/README.md).
