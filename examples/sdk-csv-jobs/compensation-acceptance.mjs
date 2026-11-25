import assert from "node:assert/strict";
import { createServer } from "node:http";
import { randomUUID } from "node:crypto";
import { writeFile, access } from "node:fs/promises";
import { join } from "node:path";
import { csvCompensation } from "./compensation.mjs";
import { queryAcceptance } from "./query-acceptance.mjs";
import { applicationToolkitAcceptance } from "./application-toolkit-acceptance.mjs";
import { refundFixture } from "./refund-acceptance.mjs";
import { csvRefund } from "./refund.mjs";
import { csvWorkInbox } from "./work-inbox.mjs";
import { liveWorkInboxAcceptance } from "./work-inbox-live-acceptance.mjs";

import { csvClarification, mappingSourceKey } from "./clarification.mjs";
import { clarificationAcceptance } from "./clarification-acceptance.mjs";

export async function compensationAcceptance({
  sdk,
  runtime,
  pool,
  client,
  files,
  storage,
  scope,
  sourceRunId,
  importId,
  home,
  env,
  cleanup,
  pythonCall,
  templates,
  driveNode,
  pause,
  reviewed = false,
  withQueries = false,
  withToolkit = false,
  withPipelineTasks = false,
  withClarification = false,
  withLiveInbox,
  afterCleanup,
}) {
  await pool.query(sdk.pipelineCompensationSchema);
  await pool.query(sdk.pipelineToolSchema);
  await pool.query(
    "CREATE TABLE sdk_csv_recovery_owners (source_run_id uuid PRIMARY KEY,import_id uuid NOT NULL UNIQUE,owner_id text NOT NULL,enabled boolean NOT NULL DEFAULT true)",
  );
  await pool.query(
    "CREATE TABLE sdk_csv_recovery_audit (input_id text PRIMARY KEY,actions integer NOT NULL CHECK(actions>0))",
  );
  await pool.query(
    "INSERT INTO sdk_csv_recovery_owners(source_run_id,import_id,owner_id) VALUES ($1,$2,$3)",
    [sourceRunId, importId, "alice"],
  );
  const jobStore = await files.jobStore({ timeoutMs: 10000 }),
    make = (reviewBaseURL) =>
      csvCompensation({
        sdk,
        runtime,
        pool,
        client,
        scope,
        jobStore,
        reviewBaseURL,
        reviewed,
      });
  let { recovery } = await make("http://127.0.0.1/");
  templates.set(recovery.workerContract.spec.name, {
    stage: "compensate",
    spec: recovery.workerContract.spec,
    id: randomUUID(),
  });
  const options = { timeoutMs: 10000 };
  const plans = await Promise.all([
    recovery.plan(sourceRunId, "alice", options),
    pythonCall({
      action: "compensation-plan",
      source_run_id: sourceRunId,
      access: "alice",
    }),
  ]);
  assert.deepEqual(plans[0], plans[1]);
  const plan = plans[0];
  assert.equal(plan.commands.length, 2);
  await assert.rejects(recovery.plan(sourceRunId, "bob", options), {
    code: "unauthorized",
  });
  assert.equal(
    (await pool.query("SELECT count(*)::int AS n FROM sdk_csv_rows")).rows[0].n,
    2,
  );
  let runId;
  let gates, clarification, retainedClarification, revisedClarification, replaceMappingSource;
  const server = createServer(async (req, res) => {
    const u = new URL(req.url, "http://localhost"),
      send = (code, value) => {
        res.writeHead(code, {
          "content-type": "application/json",
          "cache-control": "no-store",
        });
        res.end(JSON.stringify(value));
      };
    if (u.pathname === "/") {
      res.writeHead(200, {
        "content-type": "text/html",
        "cache-control": "no-store",
      });
      res.end(`<!doctype html><title>Review CSV cleanup</title><h1>Review cleanup</h1><input id="token" type="password" aria-label="Review token"><button id="attach">Load plan</button><pre id="data"></pre><button id="approve" disabled>Approve exact plan</button><p id="status"></p><script>
 let gate;const headers=()=>({'authorization':'Bearer '+document.querySelector('#token').value,'content-type':'application/json'});document.querySelector('#attach').onclick=async()=>{const r=await fetch('/gate',{headers:headers()});if(!r.ok){document.querySelector('#status').textContent='Denied';return;}gate=await r.json();document.querySelector('#data').textContent=JSON.stringify(gate.review,null,2);document.querySelector('#approve').disabled=!gate.can_decide;document.querySelector('#status').textContent='Awaiting approval';};document.querySelector('#approve').onclick=async()=>{const r=await fetch('/approve',{method:'POST',headers:headers(),body:JSON.stringify({id:gate.id,binding_sha256:gate.binding_sha256})});document.querySelector('#status').textContent=r.ok?'approved':'Denied';};</script>`);
      return;
    }
    const actor =
      req.headers.authorization === "Bearer alice-review"
        ? "alice"
        : req.headers.authorization === "Bearer bob-review"
          ? "bob"
          : null;
    if (!actor) {
      send(401, {});
      return;
    }
    try {
      if (u.pathname === "/clarification" && clarification) {
        const gate = await clarification.gates.inspect(
          u.searchParams.get("gate"),
          actor,
          options,
        );
        if (gate.kind !== "input" || gate.run_id !== u.searchParams.get("run"))
          throw new Error();
        if (req.method === "GET") {
          send(200, {
            id: gate.id,
            binding_sha256: gate.binding_sha256,
            can_decide: gate.can_decide,
            form: clarification.input.request.params,
          });
          return;
        }
        if (req.method === "POST") {
          let size = 0;
          const chunks = [];
          for await (const chunk of req) {
            size += chunk.length;
            if (size > 16384) throw new Error();
            chunks.push(chunk);
          }
          const { binding_sha256, content } = JSON.parse(
            Buffer.concat(chunks).toString(),
          );
          await clarification.input.submit(
            gate.id,
            binding_sha256,
            content,
            actor,
            options,
          );
          send(200, { submitted: true });
          return;
        }
      }
      const rows = await sdk
          .postgresPipelineGateStore((q, v) => pool.query(q, v))
          .list(runId, "alice", new AbortController().signal),
        g = rows[0];
      if (!g) {
        send(404, {});
        return;
      }
      if (u.pathname === "/gate") {
        send(200, await gates.inspect(g.intent.id, actor, options));
        return;
      }
      if (u.pathname === "/approve" && req.method === "POST") {
        const chunks = [];
        for await (const b of req) chunks.push(b);
        const body = JSON.parse(Buffer.concat(chunks).toString());
        send(
          200,
          await gates.decide(
            body.id,
            {
              binding_sha256: body.binding_sha256,
              state: "approved",
              payload: {
                reason: "Reviewed exact original rows and cleanup commands",
              },
            },
            actor,
            options,
          ),
        );
        return;
      }
    } catch {
      send(404, {});
      return;
    }
    send(404, {});
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const reviewURL = `http://127.0.0.1:${server.address().port}/`;
  cleanup.push(() => new Promise((r) => server.close(r)));
  env.GREGALE_COMP_REVIEW_URL = reviewURL;
  ({ recovery } = await make(reviewURL));
  gates = sdk.createPipelineGateService({
    scope,
    pipelineSha256: recovery.contract.sha256,
    contract: recovery.reviewContract,
    store: sdk.postgresPipelineGateStore((q, v) => pool.query(q, v)),
    transport: sdk.nativePipelineGateTransport({
      scope,
      token: "fixture-owner",
      contract: recovery.contract,
      timeoutMs: 10000,
    }),
    authorize: async (_record, _action, actor) => {
      const row = (
        await pool.query(
          "SELECT owner_id,enabled FROM sdk_csv_recovery_owners WHERE source_run_id=$1",
          [sourceRunId],
        )
      ).rows[0];
      if (!row?.enabled || row.owner_id !== actor) throw Error("unauthorized");
      return { actor_id: actor, owner_id: row.owner_id, roles: ["reviewer"] };
    },
  });
  const chrome =
    process.env.GREGALE_LOCAL_CHROME ??
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  await access(chrome);
  const { chromium } = await import(
    new URL(
      "../../sdk/node/node_modules/playwright-core/index.mjs",
      import.meta.url,
    )
  );
  const browser = await chromium.launch({
    headless: true,
    executablePath: chrome,
  });
  cleanup.push(() => browser.close());
  const page = await browser.newPage(),
    errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const context = withQueries
    ? await queryAcceptance({
        sdk,
        pool,
        scope,
        importId,
        sourceRunId,
        env,
        pythonCall,
        page,
        cleanup,
      })
    : null;
  let fixture, application, liveInbox;
  if (withToolkit) {
    fixture = await refundFixture({ sdk, pool, cleanup });
    const refund = await csvRefund({
      sdk,
      pool,
      scope,
      relayURL: fixture.relayURL,
      reviewBaseURL: fixture.reviewURL,
    });
    await pool.query(sdk.outboundActionSchema);
    await pool.query(sdk.workInboxSchema);
    const workInbox = await csvWorkInbox({
      sdk,
      runtime,
      scope,
      pool,
      cursorKey: env.GREGALE_QUERY_CURSOR_KEY,
      recovery,
      refund,
    });
    const clarificationReviewURL = reviewURL + "clarification";
    if (withClarification) {
      env.GREGALE_CSV_PREVIEW_KEY = randomUUID() + randomUUID();
      await pool.query("CREATE TABLE sdk_csv_clarification_reviewers (import_id uuid NOT NULL, actor_id text NOT NULL, enabled boolean NOT NULL DEFAULT true, PRIMARY KEY (import_id,actor_id))");
      await pool.query("CREATE TABLE sdk_csv_mapping_sources (import_id uuid PRIMARY KEY, owner_id text NOT NULL, artifact jsonb NOT NULL, enabled boolean NOT NULL DEFAULT true)");
      const key = mappingSourceKey(importId);
      storage.enableVersions(key);
      replaceMappingSource = async (text) => {
        const bytes = Buffer.from(text);
        const uploaded = await files.upload(key, sdk.blobFileSource(new Blob([bytes])), {
          timeoutMs: 10000, contentType: "text/csv",
        });
        assert.ok(uploaded.version_id);
        const artifact = {
          version: 1, scope: files.scope, owner_id: "alice", key, version_id: uploaded.version_id,
          size_bytes: bytes.length, sha256: runtime.jobHash(bytes),
        };
        await pool.query(
          "INSERT INTO sdk_csv_mapping_sources (import_id,owner_id,artifact) VALUES ($1,$2,$3) "
          + "ON CONFLICT (import_id) DO UPDATE SET artifact=excluded.artifact",
          [importId, "alice", artifact],
        );
        return artifact;
      };
      await replaceMappingSource("contact,alternate_contact\ncontact@example.test,alternate@example.test\n");
      const definition = {
        sdk,
        pool,
        client,
        files,
        jobStore,
        scope,
        importId,
        reviewBaseURL: clarificationReviewURL,
      };
      clarification = await csvClarification(definition);
      retainedClarification = await csvClarification({ ...definition, revisionStarts: "retained" });
      revisedClarification = await csvClarification({
        ...definition, version: 2, previewSigningKey: env.GREGALE_CSV_PREVIEW_KEY,
      });
      const spec = clarification.reportContract.spec;
      templates.set(spec.name, { stage: "clarification_report", spec, id: randomUUID() });
      const revisedSpec = revisedClarification.reportContract.spec;
      templates.set(revisedSpec.name, { stage: "clarification_report_v2", spec: revisedSpec, id: randomUUID() });
    }
    application = await applicationToolkitAcceptance({
      workInbox,
      sdk,
      adapters: context.adapters,
      recovery,
      refund,
      env,
      home,
      cleanup,
      importId,
      pool,
      recoveryReviewURL: reviewURL,
      refundFixture: fixture,
      withPipelineTasks,
      clarification,
      retainedClarification,
      revisedClarification,
      clarificationReviewURL,
    });
    if (withLiveInbox)
      liveInbox = await liveWorkInboxAcceptance({
        sdk,
        pool,
        env,
        cleanup,
        client,
        scope,
        inbox: workInbox,
        importId,
        ...withLiveInbox,
      });
  }
  const requestId = randomUUID(),
    mcp = sdk.createCompensationMCP(recovery);
  const nodeCall = application?.node.call ?? mcp.call;
  if (application) {
    const actual = await application.node.call(
      "recover_csv_cleanup_plan",
      { source_run_id: sourceRunId },
      "alice",
    );
    assert.deepEqual(actual.structuredContent.input, {
      plan_id: plan.identity,
      binding_sha256: plan.binding_sha256,
    });
  }
  const startName = "recover_csv_cleanup_start";
  const startArgs = {
    request_id: requestId,
    input: { plan_id: plan.identity, binding_sha256: plan.binding_sha256 },
  };
  let taskId;
  if (application?.pipelineTasks) {
    const handle = await application.node.startTask(startName, startArgs);
    assert.equal(handle.resultType, "task", JSON.stringify(handle));
    taskId = handle.taskId;
    const resumed = await application.python.task(taskId);
    assert.equal(resumed.status, "working");
    const progress = resumed._meta["io.gregale/pipeline"];
    assert.equal(progress.state, "start_unknown");
    assert.equal(progress.request_id, requestId);
    assert.equal(progress.follow_up.name, "recover_csv_cleanup_reconcile");
    const replay = await application.python.startTask(startName, startArgs);
    assert.equal(replay.taskId, taskId);
    assert.equal(
      (await pool.query("SELECT count(*)::int n FROM gregale_pipeline_tasks"))
        .rows[0].n,
      1,
    );
  } else {
    const started = await nodeCall(startName, startArgs, "alice", options);
    assert.equal(started.structuredContent.state, "start_unknown");
  }
  if (application) {
    const duplicate = await application.python.call(
      "recover_csv_cleanup_start",
      {
        request_id: requestId,
        input: { plan_id: plan.identity, binding_sha256: plan.binding_sha256 },
      },
      "alice",
    );
    assert.equal(duplicate.structuredContent.state, "start_unknown");
    assert.equal(duplicate.structuredContent.replayed, true);
    const hosts = [application.node, application.python];
    const found = await application.python.call(
      "work_inbox",
      {
        input: {
          business_ref: importId,
          kind: "pipeline",
          state: "start_unknown",
        },
      },
      "alice",
    );
    const [entry] = found.structuredContent.items;
    assert.equal(found.structuredContent.items.length, 1);
    assert.equal(entry.run_id, undefined);
    const plans = await Promise.all(
      hosts.map(async (host) => {
        const noScope = await host.followUp(entry, "alice", [
          "imports:read",
          "payments:read",
        ]);
        assert.deepEqual(noScope.structuredContent, {
          kind: "no_action",
          state: "start_unknown",
        });
        const foreign = await host.followUp(entry, "bob");
        assert.ok(foreign.isError);
        assert.deepEqual(JSON.parse(foreign.content[0].text), {
          code: "not_found",
        });
        const plan = await host.followUp(entry, "alice");
        assert.equal(plan.isError, false, JSON.stringify(plan.content));
        return plan.structuredContent;
      }),
    );
    assert.deepEqual(plans[0], plans[1]);
    const proposed = plans[0];
    assert.equal(proposed.kind, "tool_call");
    assert.equal(proposed.state, "start_unknown");
    assert.equal(proposed.name, "recover_csv_cleanup_reconcile");
    assert.equal(proposed.arguments.request_id, requestId);
    assert.equal(
      (await recovery.tool.inspect(requestId, "alice", options)).state,
      "start_unknown",
    );
    runId = proposed.arguments.run_id;
    const { record } = (
      await pool.query("SELECT record FROM sdk_pipeline_runs WHERE id=$1", [
        runId,
      ])
    ).rows[0];
    const decoyId = randomUUID();
    const decoy = {
      ...record,
      id: decoyId,
      input: {
        ...record.input,
        trigger: { ...record.input.trigger, identity: "a".repeat(64) },
      },
    };
    await pool.query("INSERT INTO sdk_pipeline_runs VALUES ($1,$2,$3)", [
      decoyId,
      decoy,
      "[]",
    ]);
    try {
      const rejected = await application.node.call(
        proposed.name,
        { ...proposed.arguments, run_id: decoyId },
        "alice",
      );
      assert.ok(rejected.isError);
      assert.deepEqual(JSON.parse(rejected.content[0].text), {
        code: "receipt_conflict",
      });
    } finally {
      await pool.query("DELETE FROM sdk_pipeline_runs WHERE id=$1", [decoyId]);
    }
    const adopted = await Promise.all(
      hosts.map((host, index) =>
        host.call(plans[index].name, plans[index].arguments, "alice"),
      ),
    );
    for (const result of adopted) {
      assert.equal(
        result.structuredContent?.state,
        "started",
        JSON.stringify(result.content),
      );
      assert.equal(result.structuredContent.receipt.run_id, runId);
    }
  } else {
    const match = await recovery.tool.findAcceptedRun(
      requestId,
      "alice",
      options,
    );
    assert.equal(match.state, "found");
    runId = match.run_id;
    const adopted = await pythonCall({
      action: "compensation-call",
      name: "recover_csv_cleanup_reconcile",
      arguments: { request_id: requestId, run_id: runId },
      access: "alice",
    });
    assert.equal(adopted.structuredContent.state, "started");
  }
  assert.equal(
    (
      await pool.query(
        "SELECT count(*)::int AS n FROM sdk_pipeline_runs WHERE record->>'workflow_name'='recover-csv-cleanup'",
      )
    ).rows[0].n,
    1,
  );
  await pool.query("UPDATE sdk_csv_recovery_owners SET enabled=false");
  assert.equal(
    (
      await nodeCall(
        "recover_csv_cleanup_status",
        { request_id: requestId },
        "alice",
        options,
      )
    ).isError,
    true,
  );
  await pool.query("UPDATE sdk_csv_recovery_owners SET enabled=true");
  let latestReply;
  let approved = false,
    complete = false,
    python = true;
  const dropped = new Set();
  // One invocation can advance only one journal entry. Leave room for both
  // commands' SDK poll budgets and dropped acknowledgements on a busy host.
  for (let invocation = 0; invocation < 500; invocation++) {
    const row = (
        await pool.query(
          "SELECT record,journal FROM sdk_pipeline_runs WHERE id=$1",
          [runId],
        )
      ).rows[0],
      path = join(home, "recovery-request.json");
    await writeFile(
      path,
      JSON.stringify({
        protocol: "gregale.workflow.code/v1",
        run_id: runId,
        workflow: recovery.contract.spec.name,
        input: row.record.input,
        invocation: invocation + 1,
        journal: row.journal,
      }),
    );
    const reply = python
      ? await pythonCall({ action: "compensation-drive", path })
      : await driveNode(path);
    python = !python;
    latestReply = reply;
    assert.notEqual(reply.result.status, "failed", JSON.stringify(reply));
    const crash = reply.ops.find(
      (op) =>
        op.name.endsWith("/submit") ||
        op.output?.state === "succeeded" ||
        op.name.endsWith("/prepare"),
    );
    if (crash && !dropped.has(crash.seq)) {
      dropped.add(crash.seq);
      continue;
    }
    if (reply.ops.some((op) => op.kind === "event") && !approved) {
      assert.equal(
        (
          await pool.query(
            "SELECT count(*)::int AS n FROM gregale_compensation_effects",
          )
        ).rows[0].n,
        0,
      );
      assert.equal(
        (
          await fetch(reviewURL + "gate", {
            headers: { authorization: "Bearer bob-review" },
          })
        ).status,
        404,
      );
      const waiting = await sdk
        .createCompensationMCP(recovery)
        .call(
          "recover_csv_cleanup_status",
          { request_id: requestId },
          "alice",
          options,
        );
      assert.equal(waiting.structuredContent.state, "awaiting_approval");
      let reviewLink = reviewURL;
      if (application) {
        // A new agent client knows the import, but carries no prior request ID.
        const args = {
          input: {
            kind: "pipeline",
            state: "awaiting_approval",
            business_ref: importId,
          },
        };
        const discovered = await application.python.call(
          "work_inbox",
          args,
          "alice",
        );
        assert.equal(
          discovered.isError,
          false,
          JSON.stringify(discovered.content),
        );
        assert.equal(
          discovered.structuredContent.items.length,
          1,
          JSON.stringify(discovered.structuredContent),
        );
        const entry = discovered.structuredContent.items[0];
        assert.equal(entry.request_id, requestId);
        assert.equal(entry.run_id, runId);
        assert.equal(entry.next_step, "open_review");
        for (const host of [application.node, application.python]) {
          const next = await host.followUp(entry, "alice");
          assert.equal(next.isError, false, JSON.stringify(next.content));
          assert.deepEqual(next.structuredContent, {
            kind: "human_review",
            state: "awaiting_approval",
            review: entry.review,
          });
          const foreign = await host.followUp(entry, "bob");
          assert.equal(foreign.isError, true);
          assert.deepEqual(JSON.parse(foreign.content[0].text), {
            code: "not_found",
          });
          reviewLink = next.structuredContent.review.review_url;
        }
        const bob = await application.node.call("work_inbox", args, "bob");
        assert.deepEqual(bob.structuredContent, {
          items: [],
          next_cursor: null,
        });
        await pool.query("UPDATE sdk_csv_recovery_owners SET enabled=false");
        try {
          const revoked = await application.python.call(
            "work_inbox",
            args,
            "alice",
          );
          assert.deepEqual(revoked.structuredContent.items, []);
          for (const host of [application.node, application.python]) {
            const denied = await host.followUp(entry, "alice");
            assert.equal(denied.isError, true);
            assert.deepEqual(JSON.parse(denied.content[0].text), {
              code: "not_found",
            });
          }
        } finally {
          await pool.query("UPDATE sdk_csv_recovery_owners SET enabled=true");
        }
        assert.equal(reviewLink, waiting.structuredContent.approval.review_url);
      }
      if (taskId) {
        for (const host of [application.node, application.python]) {
          const { data } = await host.inputTask(taskId);
          assert.equal(data.error, undefined, JSON.stringify(data));
          const task = data.result;
          assert.equal(task.status, "working");
          assert.equal(task.inputRequests, undefined);
          assert.equal(
            task._meta["io.gregale/pipeline"].state,
            "awaiting_approval",
          );
          assert.equal(
            task._meta["io.gregale/pipeline"].review.review_url,
            reviewLink,
          );
          for (const [subject, scopes] of [
            ["bob", undefined],
            ["alice", ["imports:read"]],
          ]) {
            const denied = await host.taskAccess(taskId, subject, scopes);
            assert.equal(denied.data.result, undefined);
            assert.equal(
              denied.data.error.code,
              host === application.node ? -32602 : -32002,
              JSON.stringify(denied),
            );
          }
        }
        await pool.query("UPDATE sdk_csv_recovery_owners SET enabled=false");
        try {
          for (const host of [application.node, application.python]) {
            const denied = await host.taskAccess(taskId, "alice");
            assert.equal(denied.data.result, undefined);
            assert.equal(
              denied.data.error.code,
              host === application.node ? -32602 : -32002,
              JSON.stringify(denied),
            );
          }
        } finally {
          await pool.query("UPDATE sdk_csv_recovery_owners SET enabled=true");
        }
      }
      await liveInbox?.pendingReview();
      await page.goto(reviewLink);
      await page.fill("#token", "alice-review");
      await page.click("#attach");
      await page.waitForFunction(
        () =>
          document.querySelector("#status").textContent === "Awaiting approval",
      );
      assert.ok(
        (await page.textContent("#data")).includes(plan.binding_sha256),
      );
      await page.click("#approve");
      await page.waitForFunction(
        () => document.querySelector("#status").textContent === "approved",
      );
      // A later user edit must survive compensation, even after review.
      if (!afterCleanup)
        await pool.query("UPDATE sdk_csv_rows SET email=$2 WHERE input_id=$1", [
          importId + ":1",
          "edited@example.test",
        ]);
      approved = true;
    }
    for (const op of reply.ops) {
      if (op.kind === "event") {
        const event = (
          await pool.query(
            "SELECT body FROM sdk_pipeline_events WHERE run_id=$1",
            [runId],
          )
        ).rows[0];
        assert.ok(event);
        row.journal[op.seq] = {
          ...op,
          status: "succeeded",
          attempt: 1,
          output: event.body.payload,
        };
      } else
        row.journal[op.seq] = {
          ...op,
          status: op.kind === "sleep" ? "succeeded" : op.status,
          attempt: 1,
        };
    }
    if (reply.result.status === "succeeded") {
      row.record.status = "succeeded";
      row.record.output = reply.result.output;
      complete = true;
    }
    await pool.query(
      "UPDATE sdk_pipeline_runs SET record=$2,journal=$3 WHERE id=$1",
      [runId, row.record, JSON.stringify(row.journal)],
    );
    if (complete) break;
    await pause(10);
  }
  assert.equal(
    complete,
    true,
    JSON.stringify({
      result: latestReply?.result.status,
      operations: latestReply?.ops.map((op) => ({
        name: op.name,
        status: op.status,
        kind: op.kind,
        state: op.output?.state,
      })),
    }),
  );
  assert.deepEqual(errors, []);
  const typed = await recovery.tool.result(requestId, "alice", options);
  if (taskId) {
    for (const host of [application.node, application.python]) {
      const completed = await host.task(taskId);
      assert.equal(completed.status, "completed");
      assert.deepEqual(completed.result.structuredContent.output, typed);
    }
  }
  assert.equal(typed.state, afterCleanup ? "completed" : "needs_review");
  assert.deepEqual(
    typed.results.map((r) => r.outcome.state),
    afterCleanup
      ? ["compensated", "compensated"]
      : ["compensated", "needs_review"],
  );
  const remaining = (
    await pool.query("SELECT input_id,email FROM sdk_csv_rows")
  ).rows;
  assert.deepEqual(
    remaining,
    afterCleanup
      ? []
      : [{ input_id: importId + ":1", email: "edited@example.test" }],
  );
  const audit = (await pool.query("SELECT actions FROM sdk_csv_recovery_audit"))
    .rows;
  assert.deepEqual(
    audit,
    afterCleanup ? [{ actions: 1 }, { actions: 1 }] : [{ actions: 1 }],
  );
  const nodeEffects = await recovery.effects(plan.identity, "alice", options),
    pythonEffects = await pythonCall({
      action: "compensation-effects",
      plan_id: plan.identity,
      access: "alice",
    });
  assert.deepEqual(nodeEffects, pythonEffects);
  assert.equal(nodeEffects.length, 2);
  assert.equal(
    (
      await pool.query(
        "SELECT count(*)::int AS n FROM gregale_compensation_effects",
      )
    ).rows[0].n,
    2,
  );
  const pyResult = await pythonCall({
    action: "compensation-call",
    name: "recover_csv_cleanup_result",
    arguments: { request_id: requestId },
    access: "alice",
    review_base_url: reviewURL,
  });
  assert.deepEqual(pyResult.structuredContent.output, typed);
  if (application) {
    const resumed = await application.python.call(
      "recover_csv_cleanup_result",
      { request_id: requestId },
      "alice",
    );
    assert.deepEqual(resumed.structuredContent.output, typed);
  }
  if (clarification)
    await clarificationAcceptance({
      application,
      sdk,
      clarification,
      revisedClarification,
      replaceMappingSource,
      importId,
      pool,
      storage,
    });
  if (afterCleanup)
    await afterCleanup({
      browser,
      page,
      importId,
      fixture,
      application,
      liveInbox,
      recoveryCheckpoint: (
        await recovery.tool.inspect(requestId, "alice", options)
      ).receipt.checkpoint,
    });
}
