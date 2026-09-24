package templates

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func runNodeStarter(t *testing.T, name, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is not installed")
	}
	dest := filepath.Join(t.TempDir(), name)
	if err := Materialize(name, dest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, filepath.Join(dest, "handler.js"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s starter failed: %v\n%s", name, err, output)
	}
}

func TestEventWorkerStarterReadsParsedAndRawEnvelope(t *testing.T) {
	runNodeStarter(t, "event-worker", `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { handler } = await import(pathToFileURL(process.argv[1]).href);
const envelope = { id: "event-1", source: "billing.test", type: "invoice.paid", data: { amount: 150 } };
for (const body of [envelope, JSON.stringify(envelope)]) {
  const logs = [];
  const result = await handler({ body }, { log: { info: (...args) => logs.push(args) } });
  assert.equal(result.statusCode, 202);
  assert.deepEqual(logs[0][1], { event_id: "event-1", source: "billing.test", type: "invoice.paid" });
}
await assert.rejects(() => handler({ body: { data: {} } }, { log: { info() {} } }), /missing id, source, or type/);
await assert.rejects(() => handler({ body: "not-json" }, { log: { info() {} } }), /not valid JSON/);
`)
}

func TestQueueWorkerStarterReadsParsedAndRawPayload(t *testing.T) {
	runNodeStarter(t, "queue-worker", `
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { handler } = await import(pathToFileURL(process.argv[1]).href);
for (const body of [{ job_id: "order-123" }, JSON.stringify({ job_id: "order-123" })]) {
  const logs = [];
  const result = await handler({ body }, { invocation_id: "inv-1", log: { info: (...args) => logs.push(args) } });
  assert.equal(result.statusCode, 202);
  assert.deepEqual(JSON.parse(result.body), { accepted: true, job_id: "order-123" });
  assert.equal(logs[0][1].job_id, "order-123");
}
await assert.rejects(() => handler({ body: "not-json" }, { invocation_id: "inv-1", log: { info() {} } }), /not valid JSON/);
`)
}
