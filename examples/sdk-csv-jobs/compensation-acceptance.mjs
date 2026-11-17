import assert from "node:assert/strict";
import { createServer } from "node:http";
import { randomUUID } from "node:crypto";
import { writeFile, access } from "node:fs/promises";
import { join } from "node:path";
import { csvCompensation } from "./compensation.mjs";

export async function compensationAcceptance({
  sdk,
  runtime,
  pool,
  client,
  files,
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
  const requestId = randomUUID(),
    mcp = sdk.createCompensationMCP(recovery);
  const started = await mcp.call(
    "recover_csv_cleanup_start",
    {
      request_id: requestId,
      input: { plan_id: plan.identity, binding_sha256: plan.binding_sha256 },
    },
    "alice",
    options,
  );
  assert.equal(started.structuredContent.state, "start_unknown");
  const runId = (
    await pool.query(
      "SELECT id FROM sdk_pipeline_runs WHERE record->>'workflow_name'='recover-csv-cleanup'",
    )
  ).rows[0].id;
  const adopted = await pythonCall({
    action: "compensation-call",
    name: "recover_csv_cleanup_reconcile",
    arguments: { request_id: requestId, run_id: runId },
    access: "alice",
  });
  assert.equal(adopted.structuredContent.state, "started");
  await pool.query("UPDATE sdk_csv_recovery_owners SET enabled=false");
  assert.equal(
    (
      await mcp.call(
        "recover_csv_cleanup_status",
        { request_id: requestId },
        "alice",
        options,
      )
    ).isError,
    true,
  );
  await pool.query("UPDATE sdk_csv_recovery_owners SET enabled=true");
  let gates;
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
  let approved = false,
    complete = false,
    python = true;
  const dropped = new Set();
  for (let invocation = 0; invocation < 100; invocation++) {
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
      await page.goto(reviewURL);
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
  assert.equal(complete, true);
  assert.deepEqual(errors, []);
  const typed = await recovery.tool.result(requestId, "alice", options);
  assert.equal(typed.state, "needs_review");
  assert.deepEqual(
    typed.results.map((r) => r.outcome.state),
    ["compensated", "needs_review"],
  );
  const remaining = (
    await pool.query("SELECT input_id,email FROM sdk_csv_rows")
  ).rows;
  assert.deepEqual(remaining, [
    { input_id: importId + ":1", email: "edited@example.test" },
  ]);
  const audit = (await pool.query("SELECT actions FROM sdk_csv_recovery_audit"))
    .rows;
  assert.deepEqual(audit, [{ actions: 1 }]);
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
}
