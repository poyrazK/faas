import assert from "node:assert/strict";
import { generateKeyPairSync, randomUUID } from "node:crypto";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { writeFile } from "node:fs/promises";
import { delimiter, join } from "node:path";
import { createInterface } from "node:readline";
import { setTimeout as delay } from "node:timers/promises";
import { pathToFileURL } from "node:url";
import { runWorkflowInvocation } from "../../sdk/node/dist/workflows.js";
import { audience, claims, config, ownerClient } from "./application-toolkit-acceptance.mjs";
import { csvPipelineTool } from "./pipeline-tool.mjs";

export async function assignedApprovalAcceptance({
  sdk, runtime, scope, files, contract, pipeline, gateStore, reviewContract, source, importId, pool, home, env, python, cleanup,
}) {
  assert.ok(source.version_id);
  const options = { timeoutMs: 10000 }, ownerScopes = ["imports:write", "imports:read"];
  const reviewerScopes = ["imports:handoff", "imports:approve"], reviewer = "reviewer";
  const query = (statement, values) => pool.query(statement, values);
  await pool.query(sdk.pipelineTaskSchema);
  await pool.query(sdk.pipelineTaskHandoffSchema);
  await pool.query("CREATE TABLE sdk_csv_approval_members (actor text, owner text, enabled boolean NOT NULL, PRIMARY KEY (actor, owner))");
  await pool.query("INSERT INTO sdk_csv_approval_members VALUES ($1,'alice',true)", [reviewer]);

  const pipelineTool = await csvPipelineTool({
    sdk, scope, gateStore, token: env.FAAS_TOKEN, valueHash: runtime.jobValueHash,
    store: sdk.postgresPipelineToolStore(query), reviewBaseURL: "https://app.example.test/review",
    loadImport: async (id) => (await query("SELECT * FROM sdk_csv_tool_imports WHERE request_id=$1", [id])).rows[0],
  });
  const toolkit = sdk.createApplicationToolkit([{
    adapter: sdk.createPipelineToolMCP(pipelineTool),
    toolScopes: { csv_import_start: ["imports:write"], csv_import_status: ["imports:read"], csv_import_result: ["imports:read"] },
  }]);
  const gates = sdk.createPipelineGateService({
    scope, pipelineSha256: contract.sha256, contract: reviewContract, store: gateStore,
    transport: sdk.nativePipelineGateTransport({ scope, token: env.FAAS_TOKEN, contract, timeoutMs: 5000 }),
    async authorize(record, _action, actor) {
      const owner = record.intent.owner_id;
      if (actor !== owner && !(await query("SELECT 1 FROM sdk_csv_approval_members WHERE actor=$1 AND owner=$2 AND enabled", [actor, owner])).rowCount)
        throw new sdk.PipelineGateError("forbidden");
      return { actor_id: actor, owner_id: owner, roles: ["reviewer"] };
    },
  });
  const settings = { namespace: "csv-assigned-approval", ownerKey: randomUUID() + randomUUID(), tools: ["csv_import"] };
  const tasks = sdk.createPipelineTasks(toolkit, {
    ...settings, store: sdk.postgresPipelineTaskStore(query),
    handoffs: { store: sdk.postgresPipelineTaskHandoffStore(query), scopes: ["imports:handoff"] },
    approvalHandoffs: { csv_import: sdk.createPipelineTaskApproval({
      contract: reviewContract, service: gates, title: "Import validated rows", scopes: ["imports:approve"],
    }) },
  });
  const { privateKey, publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const root = new URL("../../", import.meta.url);
  const nodePath = process.env.GREGALE_MCP_STARTER_NODE ?? new URL("cmd/gregale/templates/mcp-node-tasks/", root).pathname;
  const { createApp } = await import(pathToFileURL(join(nodePath, "app.js")));
  const app = createApp(config, { toolkit, pipelineTasks: tasks, keyResolver: publicKey, log() {} });
  const server = app.app.listen(0, "127.0.0.1");
  await once(server, "listening");
  cleanup.push(async () => {
    app.closeTaskSubscriptions();
    await app.handler.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  });

  const pySettings = join(home, "approval-host.json");
  await writeFile(pySettings, JSON.stringify([
    config, publicKey.export({ format: "jwk" }), null, null, null,
    { ...settings, reviewedApproval: { root: contract.spec, review: reviewContract.spec } },
  ]));
  const pyEnv = { ...env, PYTHONPATH: [env.PYTHONPATH, process.env.GREGALE_MCP_STARTER_PYTHONPATH].filter(Boolean).join(delimiter) };
  const pyHost = spawn(python, [
    new URL("./application_toolkit_fixture.py", import.meta.url).pathname,
    new URL("cmd/gregale/templates/mcp-python-tasks/", root).pathname, pySettings,
  ], { env: pyEnv, stdio: ["ignore", "pipe", "pipe"] });
  let pyErrors = "";
  pyHost.stderr.on("data", (data) => { pyErrors += data; });
  const pyLines = createInterface({ input: pyHost.stdout });
  cleanup.push(async () => {
    pyLines.close();
    if (pyHost.exitCode === null && pyHost.signalCode === null) {
      const closed = once(pyHost, "close");
      pyHost.kill("SIGTERM");
      await closed;
    }
  });
  const pyPort = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("Python approval host startup timed out")), 30000);
    pyHost.once("exit", () => { clearTimeout(timer); reject(new Error(pyErrors)); });
    pyHost.once("error", (error) => { clearTimeout(timer); reject(error); });
    pyLines.once("line", (line) => { clearTimeout(timer); resolve(JSON.parse(line).port); });
  });

  const transport = (subject, scopes, clientId) => sdk.createPipelineTaskHttpTransport({
    endpoint: "http://127.0.0.1:" + server.address().port + "/mcp",
    fetch(url, init) {
      const headers = new Headers(init.headers);
      headers.set("Authorization", "Bearer " + claims(privateKey, subject, scopes, clientId));
      return fetch(url, { ...init, headers });
    },
  });
  const owner = sdk.createPipelineTaskClient({ tool: "csv_import", contract, transport: transport("alice", ownerScopes, ownerClient) });
  const ownerCaller = { resource: audience, subject: "alice", clientId: ownerClient, access: { principal: "alice", scopes: ownerScopes } };
  const reviewerTransport = transport(reviewer, reviewerScopes, "node-reviewer");
  const inbox = sdk.createPipelineTaskReviewerInbox(reviewerTransport);
  const forGrant = (grant, subject = reviewer, scopes = reviewerScopes) => sdk.createPipelineTaskApprovalClient({
    handoffId: grant.handoff_id, contract: reviewContract, transport: transport(subject, scopes, "node-reviewer"),
  });
  async function pythonReview(action, grant, extra = {}) {
    const child = spawn(python, [new URL("./pipeline_task_driver.py", import.meta.url).pathname], {
      env: pyEnv, stdio: ["pipe", "pipe", "pipe"], signal: AbortSignal.timeout(30000),
    });
    let stdout = "", stderr = "";
    child.stdout.on("data", (data) => { stdout += data; });
    child.stderr.on("data", (data) => { stderr += data; });
    child.stdin.end(JSON.stringify({
      action, endpoint: "http://127.0.0.1:" + pyPort + "/mcp",
      token: claims(privateKey, reviewer, reviewerScopes, "python-reviewer"),
      handoff_id: grant.handoff_id, review_contract: reviewContract.spec, ...extra,
    }));
    const [code] = await once(child, "close");
    assert.equal(code, 0, stderr);
    return JSON.parse(stdout);
  }

  async function advance(runId) {
    const { record, journal } = (await query("SELECT record,journal FROM sdk_pipeline_runs WHERE id=$1", [runId])).rows[0];
    const deadline = performance.now() + 90000;
    for (let invocation = journal.length + 1; performance.now() < deadline; invocation++) {
      const response = await runWorkflowInvocation(pipeline.definition, {
        protocol: "gregale.workflow.code/v1", run_id: runId, workflow: record.workflow_name,
        input: record.input, invocation, journal,
      });
      assert.notEqual(response.result.status, "failed", JSON.stringify(response));
      record.status = "running";
      for (const op of response.ops) {
        assert.notEqual(op.status, "failed", JSON.stringify(op));
        if (op.kind === "event") {
          const event = (await query("SELECT body FROM sdk_pipeline_events WHERE run_id=$1", [runId])).rows[0];
          if (!event) { record.status = "awaiting_event"; break; }
          journal[op.seq] = { ...op, status: "succeeded", attempt: 1, output: event.body.payload };
        } else {
          journal[op.seq] = { ...op, status: op.kind === "sleep" ? "succeeded" : op.status, attempt: 1 };
        }
      }
      if (response.result.status === "succeeded") {
        record.status = "succeeded";
        record.output = response.result.output;
      }
      await query("UPDATE sdk_pipeline_runs SET record=$2,journal=$3 WHERE id=$1", [runId, record, JSON.stringify(journal)]);
      if (record.status !== "running") return record.output?.output;
      if (response.ops.some((op) => op.kind === "sleep")) await delay(250);
    }
    assert.fail("The reviewed import did not reach its next state: " + JSON.stringify(journal.slice(-5).map(({ name, status }) => ({ name, status }))));
  }

  async function stage() {
    const input = { import_id: importId, source }, requestId = randomUUID();
    await query("INSERT INTO sdk_csv_tool_imports(request_id,owner_id,input) VALUES ($1,'alice',$2)", [requestId, input]);
    const task = owner.resume(await owner.start(input, requestId, options));
    const runId = (await task.get(options)).progress.run_id;
    await advance(runId);
    assert.equal((await task.get(options)).progress.state, "awaiting_approval");
    const grant = await tasks.grantHandoff(task.checkpoint.task_id, { reviewerSubject: reviewer, ttlMs: 300000 }, ownerCaller, options);
    return { task, runId, grant, approval: forGrant(grant) };
  }

  const importCount = async () => (await pool.query("SELECT count(*)::int AS n FROM sdk_csv_rows")).rows[0].n;
  const wakes = async (runId) => (await query("SELECT count(*)::int AS n FROM sdk_pipeline_events WHERE run_id=$1", [runId])).rows[0].n;
  const reject = await stage();
  assert.deepEqual((await inbox.page(options)).items, []);
  const watch = inbox.watch({ ...options, includeApprovals: true });
  try {
    assert.deepEqual((await watch.next()).value.items, [{
      handoff_id: reject.grant.handoff_id, expires_at: reject.grant.expires_at, question: "Import validated rows", kind: "approval",
    }]);
  } finally { await watch.return(); }
  await assert.rejects(forGrant(reject.grant, "bob").get(options), { code: "not_found" });
  await assert.rejects(forGrant(reject.grant, reviewer, ["imports:handoff"]).get(options), { code: "not_found" });
  const rejectedView = await reject.approval.get(options);
  assert.deepEqual(rejectedView.review.source, source);
  assert.equal(rejectedView.review.rows.length, 3);
  assert.deepEqual(await pythonReview("approval-read", reject.grant), rejectedView);
  const denied = await reject.approval.decide(rejectedView, "rejected", { reason: "CSV needs correction" }, options);
  assert.equal(denied.state, "rejected");
  assert.deepEqual(await advance(reject.runId), { status: "rejected", saved: 0, rejected: 0 });
  assert.equal(await importCount(), 0);

  const approved = await stage();
  const expired = await tasks.grantHandoff(approved.task.checkpoint.task_id, { reviewerSubject: reviewer, ttlMs: 300000 }, ownerCaller, options);
  const expiredClient = forGrant(expired), expiredView = await expiredClient.get(options);
  await query("UPDATE gregale_pipeline_task_handoffs SET expires_at=clock_timestamp() WHERE id=$1", [expired.handoff_id]);
  await assert.rejects(expiredClient.decide(expiredView, "approved", { reason: "Late" }, options), { code: "not_found" });
  assert.equal(await wakes(approved.runId), 0);

  const view = await approved.approval.get(options);
  const value = { reason: "Reviewed the pinned CSV and exact rows" };
  await assert.rejects(approved.approval.decide({ ...view, decision_key: rejectedView.decision_key }, "approved", value, options), { code: "invalid_input" });
  await query("UPDATE sdk_csv_approval_members SET enabled=false");
  await assert.rejects(approved.approval.decide(view, "approved", value, options), { code: "not_found" });
  assert.deepEqual((await inbox.page({ ...options, includeApprovals: true })).items, []);
  assert.equal(await importCount(), 0);
  await query("UPDATE sdk_csv_approval_members SET enabled=true");
  const accepted = await pythonReview("approval-decide", approved.grant, { view, state: "approved", input: value });
  assert.equal(accepted.state, "approved");
  assert.deepEqual(await approved.approval.decide(view, "approved", value, options), accepted);
  assert.equal(await wakes(approved.runId), 1);
  const [decidedGate] = await gateStore.list(approved.runId, "alice", AbortSignal.timeout(10000));
  assert.equal(decidedGate.decision.actor_id, reviewer);
  const completed = await advance(approved.runId);
  assert.equal(completed.saved, 2);
  assert.equal((await approved.task.get(options)).output.status, "completed");
  assert.equal(await importCount(), 2);
  const report = await sdk.readPipelineArtifact(files, completed.report, "alice", { ...options, maxBytes: 65536 });
  assert.match(new TextDecoder().decode(report), /invalid_email/);

  const racing = await stage();
  const raceView = await racing.approval.get(options);
  const outcomes = await Promise.all([
    racing.approval.decide(raceView, "approved", value, options).catch((error) => ({ error: error.code })),
    pythonReview("approval-decide", racing.grant, { view: raceView, state: "rejected", input: { reason: "Wait" } }),
  ]);
  assert.equal(outcomes.filter((outcome) => outcome.state).length, 1);
  assert.equal(outcomes.find((outcome) => outcome.error)?.error, "invalid_input");
  assert.equal(await wakes(racing.runId), 1);
}
