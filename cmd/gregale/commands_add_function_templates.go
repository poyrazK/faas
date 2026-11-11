package main

import (
	"fmt"
	"path"
	"strings"
)

// renderFunctionScaffold returns the handler (first) and its test.
func renderFunctionScaffold(o addFunctionOptions, root string) []scaffoldFile {
	if o.Lang == "python" {
		return []scaffoldFile{
			{Rel: path.Join(root, "handler.py"), Body: pythonHandler(o)},
			{Rel: path.Join(root, "test_handler.py"), Body: pythonTest(o)},
		}
	}
	return []scaffoldFile{
		{Rel: path.Join(root, "handler.js"), Body: nodeHandler(o)},
		{Rel: path.Join(root, "handler.test.js"), Body: nodeTest(o)},
		// ES module syntax for local `node --test` on every Node version.
		{Rel: path.Join(root, "package.json"), Body: fmt.Sprintf("{\n  \"name\": %q,\n  \"private\": true,\n  \"type\": \"module\"\n}\n", o.Name)},
	}
}

func webhookSecretEnv(provider string) string {
	return strings.ToUpper(provider) + "_WEBHOOK_SECRET"
}

// nodeConfig is the handler's config literal (camelCase keys).
func nodeConfig(o addFunctionOptions) string {
	switch o.Trigger {
	case "cron":
		return fmt.Sprintf("{ cron: %q }", o.Schedule)
	case "queue":
		return fmt.Sprintf("{ queue: %q }", o.Queue)
	case "bucket":
		return fmt.Sprintf("{\n  bucketEvents: [{ bucket: %q, events: [\"s3:ObjectCreated:*\"] }],\n}", o.Bucket)
	case "webhook":
		return fmt.Sprintf("{\n  // Export %s when you deploy; the endpoint URL is printed once.\n  webhooks: [{ name: %q, provider: %q, signingSecretEnv: %q }],\n}", webhookSecretEnv(o.Provider), o.Provider, o.Provider, webhookSecretEnv(o.Provider))
	}
	return "{\n  // Settings such as ramMb, requestTimeoutS, cron or queue go here.\n}"
}

// pythonConfig is the handler's config literal (snake_case keys).
func pythonConfig(o addFunctionOptions) string {
	switch o.Trigger {
	case "cron":
		return fmt.Sprintf("{\"cron\": %q}", o.Schedule)
	case "queue":
		return fmt.Sprintf("{\"queue\": %q}", o.Queue)
	case "bucket":
		return fmt.Sprintf("{\n    \"bucket_events\": [{\"bucket\": %q, \"events\": [\"s3:ObjectCreated:*\"]}],\n}", o.Bucket)
	case "webhook":
		return fmt.Sprintf("{\n    # Export %s when you deploy; the endpoint URL is printed once.\n    \"webhooks\": [{\"name\": %q, \"provider\": %q, \"signing_secret_env\": %q}],\n}", webhookSecretEnv(o.Provider), o.Provider, o.Provider, webhookSecretEnv(o.Provider))
	}
	return "{\n    # Settings such as ram_mb, request_timeout_s, cron or queue go here.\n}"
}

const scaffoldHeaderNode = `// %s: a Gregale function. ` + "`gregale deploy`" + ` reads config below
// without running this file (docs/functions.md#config-in-code).
`

func nodeHandler(o addFunctionOptions) string {
	head := fmt.Sprintf(scaffoldHeaderNode, o.Name) + "export const config = " + nodeConfig(o) + ";\n\n"
	switch o.Trigger {
	case "http":
		return head + `export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    return Response.json({ hello: "world", path: url.pathname });
  },
};
`
	case "cron":
		return head + `export async function handler(event, ctx) {
  // Cron calls arrive with the header x-faas-invocation-source: cron.
  console.log("scheduled run", ctx.invocation_id);
  return { status: 200, body: { ok: true } };
}
`
	case "queue":
		return head + `export async function handler(event, ctx) {
  // event.body is one queue message (parsed JSON when it is JSON).
  // Delivery is at least once: make this safe to repeat.
  const message = event.body;
  console.log("message", ctx.invocation_id, typeof message);
  return { status: 200, body: { ok: true } };
}
`
	case "bucket":
		return head + `export async function handler(event, ctx) {
  // event.body is an S3-style notification: { Records: [...] }.
  const records = event.body?.Records ?? [];
  for (const record of records) {
    console.log(record.eventName, record.s3?.object?.key);
  }
  return { status: 200, body: { processed: records.length } };
}
`
	}
	return head + `export async function handler(event, ctx) {
  // Verified deliveries carry x-faas-invocation-source: inbound_webhook;
  // event.body is the provider's JSON payload.
  if (event.headers["x-faas-invocation-source"] !== "inbound_webhook") {
    return { status: 403, body: { error: "not a webhook delivery" } };
  }
  const payload = event.body;
  console.log("webhook", event.headers["x-gregale-webhook-event-id"], payload?.type);
  return { status: 200, body: { received: true } };
}
`
}

func nodeTest(o addFunctionOptions) string {
	head := `// Run with: node --test
import test from "node:test";
import assert from "node:assert/strict";
`
	if o.Trigger == "http" {
		return head + `import handler from "./handler.js";

test("answers", async () => {
  const res = await handler.fetch(new Request("http://faas.local/hello"), {}, {});
  assert.equal(res.status, 200);
  assert.deepEqual(await res.json(), { hello: "world", path: "/hello" });
});
`
	}
	return head + `import { handler } from "./handler.js";

const ctx = { invocation_id: "test" };
const event = (body, headers = {}) => ({ method: "POST", path: "/", headers, query: "", body, body_b64: "" });

test("handles a delivery", async () => {
  const res = await handler(event(` + nodeTestBody(o) + `), ctx);
  assert.equal(res.status, 200);
});
`
}

func nodeTestBody(o addFunctionOptions) string {
	switch o.Trigger {
	case "bucket":
		return `{ Records: [{ eventName: "ObjectCreated:Put", s3: { object: { key: "a.png" } } }] }`
	case "webhook":
		return `{ type: "test.event" }, { "x-faas-invocation-source": "inbound_webhook" }`
	case "queue":
		return `{ id: 1 }`
	}
	return `null, { "x-faas-invocation-source": "cron" }`
}

func pythonHandler(o addFunctionOptions) string {
	head := fmt.Sprintf("# %s: a Gregale function. `gregale deploy` reads config below\n# without running this file (docs/functions.md#config-in-code).\n", o.Name) +
		"config = " + pythonConfig(o) + "\n\n\n"
	switch o.Trigger {
	case "http":
		return head + `def handler(event, ctx):
    return {
        "status": 200,
        "headers": {"content-type": "application/json"},
        "body": {"hello": "world", "path": event["path"]},
    }
`
	case "cron":
		return head + `def handler(event, ctx):
    # Cron calls arrive with the header x-faas-invocation-source: cron.
    print("scheduled run", ctx.invocation_id)
    return {"status": 200, "body": {"ok": True}}
`
	case "queue":
		return head + `def handler(event, ctx):
    # event["body"] is one queue message (parsed JSON when it is JSON).
    # Delivery is at least once: make this safe to repeat.
    message = event["body"]
    print("message", ctx.invocation_id, type(message).__name__)
    return {"status": 200, "body": {"ok": True}}
`
	case "bucket":
		return head + `def handler(event, ctx):
    # event["body"] is an S3-style notification: {"Records": [...]}.
    records = (event["body"] or {}).get("Records", [])
    for record in records:
        print(record.get("eventName"), record.get("s3", {}).get("object", {}).get("key"))
    return {"status": 200, "body": {"processed": len(records)}}
`
	}
	return head + `def handler(event, ctx):
    # Verified deliveries carry x-faas-invocation-source: inbound_webhook;
    # event["body"] is the provider's JSON payload.
    if event["headers"].get("x-faas-invocation-source") != "inbound_webhook":
        return {"status": 403, "body": {"error": "not a webhook delivery"}}
    payload = event["body"] or {}
    print("webhook", event["headers"].get("x-gregale-webhook-event-id"), payload.get("type"))
    return {"status": 200, "body": {"received": True}}
`
}

func pythonTest(o addFunctionOptions) string {
	body, headers := "None", "{}"
	switch o.Trigger {
	case "http":
		return `# Run with: python -m pytest
from handler import handler


def test_answers():
    event = {"method": "GET", "path": "/hello", "headers": {}, "query": "", "body": None, "body_b64": ""}
    res = handler(event, None)
    assert res["status"] == 200
    assert res["body"] == {"hello": "world", "path": "/hello"}
`
	case "bucket":
		body = `{"Records": [{"eventName": "ObjectCreated:Put", "s3": {"object": {"key": "a.png"}}}]}`
	case "webhook":
		body, headers = `{"type": "test.event"}`, `{"x-faas-invocation-source": "inbound_webhook"}`
	case "queue":
		body = `{"id": 1}`
	case "cron":
		headers = `{"x-faas-invocation-source": "cron"}`
	}
	return fmt.Sprintf(`# Run with: python -m pytest
from types import SimpleNamespace

from handler import handler


def test_handles_a_delivery():
    event = {"method": "POST", "path": "/", "headers": %s, "query": "", "body": %s, "body_b64": ""}
    res = handler(event, SimpleNamespace(invocation_id="test"))
    assert res["status"] == 200
`, headers, body)
}
