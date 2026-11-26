import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { csvRefund } from "./refund.mjs";

export async function refundFixture({ sdk, pool, cleanup }) {
  let gates, runId, refund;
  let posts = 0;
  const options = { timeoutMs: 20000 };
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://localhost"),
      send = (code, value) => {
        res.writeHead(code, {
          "content-type": "application/json",
          "cache-control": "no-store",
        });
        res.end(JSON.stringify(value));
      };
    if (url.pathname.startsWith("/relay/")) {
      assert.equal(req.headers.authorization, undefined);
      assert.equal(req.headers["stripe-version"], "2026-10-28.endive");
      if (url.pathname.endsWith("/v1/account")) {
        send(200, { id: "acct_fixture" });
        return;
      }
      if (req.method === "POST") {
        posts++;
        const chunks = [];
        for await (const b of req) chunks.push(b);
        const form = new URLSearchParams(Buffer.concat(chunks).toString());
        assert.equal(
          req.headers["idempotency-key"],
          "gregale-action:" + form.get("metadata[gregale_action_id]"),
        );
        refund = {
          id: "re_csv",
          object: "refund",
          payment_intent: form.get("payment_intent"),
          amount: Number(form.get("amount")),
          currency: form.get("currency"),
          status: "succeeded",
          metadata: {
            gregale_action_id: form.get("metadata[gregale_action_id]"),
            gregale_input_sha256: form.get("metadata[gregale_input_sha256]"),
          },
        };
        res.destroy();
        return; // Provider accepted, caller lost its response.
      }
      if (url.pathname.endsWith("/v1/refunds")) {
        send(200, {
          object: "list",
          has_more: false,
          data: refund ? [refund] : [],
        });
        return;
      }
      send(200, refund);
      return;
    }
    if (url.pathname === "/") {
      res.writeHead(200, {
        "content-type": "text/html",
        "cache-control": "no-store",
      });
      res.end(
        `<!doctype html><title>Review CSV refund</title><h1>Approve refund</h1><input id="token" type="password" aria-label="Review token"><button id="load">Load refund</button><pre id="data"></pre><button id="approve" disabled>Approve exact refund</button><p id="status"></p><script>let gate;const headers=()=>({'authorization':'Bearer '+document.querySelector('#token').value,'content-type':'application/json'});document.querySelector('#load').onclick=async()=>{const r=await fetch('/gate',{headers:headers()});if(!r.ok){document.querySelector('#status').textContent='Denied';return;}gate=await r.json();document.querySelector('#data').textContent=JSON.stringify(gate.review,null,2);document.querySelector('#approve').disabled=!gate.can_decide;document.querySelector('#status').textContent='Awaiting approval';};document.querySelector('#approve').onclick=async()=>{const r=await fetch('/approve',{method:'POST',headers:headers(),body:JSON.stringify({id:gate.id,binding_sha256:gate.binding_sha256})});document.querySelector('#status').textContent=r.ok?'approved':'Denied';};</script>`,
      );
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
    if (!gates || !runId) {
      send(404, {});
      return;
    }
    try {
      const rows = await sdk
          .postgresPipelineGateStore((s, v) => pool.query(s, v))
          .list(runId, "alice", new AbortController().signal),
        gate = rows[0];
      if (!gate) {
        send(404, {});
        return;
      }
      if (url.pathname === "/gate") {
        send(200, await gates.inspect(gate.intent.id, actor, options));
        return;
      }
      if (url.pathname === "/approve" && req.method === "POST") {
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
                reason:
                  "Reviewed exact payment, amount and currency after cleanup",
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
  cleanup.push(() => new Promise((r) => server.close(r)));
  const reviewURL = `http://127.0.0.1:${server.address().port}/`,
    relayURL = reviewURL + "relay";
  return {
    reviewURL,
    relayURL,
    posts: () => posts,
    attach(service, id) {
      gates = service;
      runId = id;
    },
  };
}

/** Real PostgreSQL and browser review; native control plane and Stripe are protocol fixtures. */
export async function refundAcceptance({
  sdk,
  pool,
  scope,
  home,
  env,
  cleanup,
  pythonCall,
  driveNode,
  pause,
  page,
  recoveryCheckpoint,
  importId,
  fixture,
  application,
  liveInbox,
}) {
  await pool.query(sdk.outboundActionSchema);
  await pool.query(
    await readFile(new URL("refund-schema.sql", import.meta.url), "utf8"),
  );
  const actionRequestId = randomUUID(),
    workflowRequestId = randomUUID(),
    input = {
      payment_intent: "pi_csv",
      amount_minor: 1000,
      currency: "usd",
      reason: "requested_by_customer",
    };
  await pool.query(
    "INSERT INTO sdk_csv_refund_requests(id,owner_id,payment_id,amount_minor,currency,recovery_checkpoint) VALUES ($1,$2,$3,$4,$5,$6)",
    [actionRequestId, "alice", "pi_csv", 1000, "usd", recoveryCheckpoint],
  );
  fixture ??= await refundFixture({ sdk, pool, cleanup });
  const { reviewURL, relayURL } = fixture;
  const options = { timeoutMs: 20000 };
  env.GREGALE_REFUND_RELAY_URL = relayURL;
  env.GREGALE_REFUND_REVIEW_URL = reviewURL;
  const action = await csvRefund({
      sdk,
      scope,
      pool,
      relayURL,
      reviewBaseURL: reviewURL,
    }),
    mcp = sdk.createOutboundActionMCP(action);
  const py = (request) =>
    pythonCall({ ...request, relay_url: relayURL, review_base_url: reviewURL });
  const plans = await Promise.all([
    (application?.node.call ?? mcp.call)(
      "action_csv_refund_prepare",
      { request_id: actionRequestId, input },
      "alice",
      options,
    ),
    application
      ? application.python.call(
          "action_csv_refund_prepare",
          { request_id: actionRequestId, input },
          "alice",
        )
      : py({
          action: "refund-call",
          name: "action_csv_refund_prepare",
          arguments: { request_id: actionRequestId, input },
          access: "alice",
        }),
  ]);
  assert.deepEqual(plans[0].structuredContent, plans[1].structuredContent);
  const prepared = plans[0].structuredContent,
    reference = {
      action_id: prepared.action_id,
      binding_sha256: prepared.binding_sha256,
    };
  assert.equal(
    (
      await mcp.call(
        "action_csv_refund_prepare",
        { request_id: actionRequestId, input },
        "bob",
        options,
      )
    ).isError,
    true,
  );
  await pool.query("UPDATE sdk_csv_refund_requests SET enabled=false");
  assert.equal(
    (
      await py({
        action: "refund-call",
        name: "action_csv_refund_observe",
        arguments: { action_id: prepared.action_id },
        access: "alice",
      })
    ).isError,
    true,
  );
  await pool.query("UPDATE sdk_csv_refund_requests SET enabled=true");
  await pool.query("UPDATE sdk_csv_payments SET currency='eur'");
  assert.equal(
    (
      await py({
        action: "refund-call",
        name: "action_csv_refund_observe",
        arguments: { action_id: prepared.action_id },
        access: "alice",
      })
    ).isError,
    true,
  );
  await pool.query(
    "UPDATE sdk_csv_payments SET currency='usd',amount_cents=999",
  );
  assert.equal(
    (
      await mcp.call(
        "action_csv_refund_observe",
        { action_id: prepared.action_id },
        "alice",
        options,
      )
    ).isError,
    true,
  );
  await pool.query("UPDATE sdk_csv_payments SET amount_cents=1000");
  const started = await (application?.node.call ?? mcp.call)(
    "action_csv_refund_start",
    { request_id: workflowRequestId, input: reference },
    "alice",
    options,
  );
  assert.equal(started.structuredContent.state, "start_unknown");
  const runId = (
    await pool.query(
      "SELECT id FROM sdk_pipeline_runs WHERE record->>'workflow_name'='action-csv-refund'",
    )
  ).rows[0].id;
  assert.equal(
    (
      await py({
        action: "refund-call",
        name: "action_csv_refund_reconcile",
        arguments: { request_id: workflowRequestId, run_id: runId },
        access: "alice",
      })
    ).structuredContent.state,
    "started",
  );
  const gates = sdk.createPipelineGateService({
    scope,
    pipelineSha256: action.contract.sha256,
    contract: action.reviewContract,
    store: sdk.postgresPipelineGateStore((s, v) => pool.query(s, v)),
    transport: sdk.nativePipelineGateTransport({
      scope,
      token: "fixture-owner",
      contract: action.contract,
      timeoutMs: 20000,
    }),
    authorize: async (_record, _operation, actor) => {
      const row = (
        await pool.query(
          "SELECT r.enabled,r.owner_id,p.enabled AS payment_enabled FROM sdk_csv_refund_requests r JOIN sdk_csv_payments p ON p.payment_id=r.payment_id WHERE r.id=$1",
          [actionRequestId],
        )
      ).rows[0];
      if (!row?.enabled || !row.payment_enabled || row.owner_id !== actor)
        throw Error("unauthorized");
      return { actor_id: actor, owner_id: row.owner_id, roles: ["reviewer"] };
    },
  });
  fixture.attach(gates, runId);
  let approved = false,
    complete = false,
    drop = true;
  for (let invocation = 0; invocation < 50; invocation++) {
    const row = (
        await pool.query(
          "SELECT record,journal FROM sdk_pipeline_runs WHERE id=$1",
          [runId],
        )
      ).rows[0],
      path = join(home, "refund-request.json");
    await writeFile(
      path,
      JSON.stringify({
        protocol: "gregale.workflow.code/v1",
        run_id: runId,
        workflow: action.contract.spec.name,
        input: row.record.input,
        invocation: invocation + 1,
        journal: row.journal,
      }),
    );
    const reply =
      invocation % 2 === 0
        ? await py({ action: "refund-drive", path })
        : await driveNode(path);
    assert.notEqual(reply.result.status, "failed", JSON.stringify(reply));
    if (drop && reply.ops.some((o) => o.name === "action/prepare")) {
      drop = false;
      continue;
    }
    if (!approved && reply.ops.some((o) => o.kind === "event")) {
      assert.equal(fixture.posts(), 0);
      assert.equal(
        (
          await fetch(reviewURL + "gate", {
            headers: { authorization: "Bearer bob-review" },
          })
        ).status,
        404,
      );
      await page.goto(reviewURL);
      await page.fill("#token", "alice-review");
      await page.click("#load");
      await page.waitForFunction(
        () =>
          document.querySelector("#status").textContent === "Awaiting approval",
      );
      const text = await page.textContent("#data");
      for (const value of ["pi_csv", "1000", "usd", prepared.binding_sha256])
        assert.ok(text.includes(value));
      await page.click("#approve");
      await page.waitForFunction(
        () => document.querySelector("#status").textContent === "approved",
      );
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
  assert.ok(complete && approved);
  assert.equal(fixture.posts(), 1);
  const historical = await action.tool.result(
    workflowRequestId,
    "alice",
    options,
  );
  assert.equal(historical.decision, "approved");
  assert.equal(historical.action.outcome.state, "unknown");
  let refundEntry, next;
  if (application) {
    const args = {
      input: { kind: "action", state: "unknown", business_ref: importId },
    };
    const found = await application.node.call("work_inbox", args, "alice");
    assert.equal(found.structuredContent.items.length, 1);
    const item = found.structuredContent.items[0];
    assert.equal(item.action_id, prepared.action_id);
    assert.equal(item.request_id, actionRequestId);
    assert.equal(item.next_step, "reconcile_action");
    assert.equal(item.name, "action_csv_refund");
    assert.deepEqual(
      (await application.python.call("work_inbox", args, "bob"))
        .structuredContent,
      { items: [], next_cursor: null },
    );
    refundEntry = item;
    for (const host of [application.node, application.python]) {
      const denied = await host.followUp(item, "alice", [
        "imports:read",
        "payments:read",
      ]);
      assert.equal(denied.isError, true);
      assert.deepEqual(JSON.parse(denied.content[0].text), {
        code: "not_found",
      });
      const follow = await host.followUp(item, "alice");
      assert.equal(follow.isError, false, JSON.stringify(follow.content));
      assert.deepEqual(follow.structuredContent, {
        kind: "tool_call",
        state: "unknown",
        name: "action_csv_refund_refresh",
        arguments: { action_id: item.action_id },
      });
      next = follow.structuredContent;
    }
    assert.equal(fixture.posts(), 1);
  }
  await liveInbox?.refundUnknown();
  const current = application
    ? await application.python.call(next.name, next.arguments, "alice")
    : await py({
        action: "refund-call",
        name: "action_csv_refund_refresh",
        arguments: { action_id: prepared.action_id },
        access: "alice",
      });
  assert.equal(current.structuredContent.outcome.state, "succeeded");
  assert.equal(current.structuredContent.outcome.result.refund_id, "re_csv");
  await liveInbox?.reconciled(prepared.action_id);
  await liveInbox?.revoke();
  assert.equal(fixture.posts(), 1);
  const observed = await mcp.call(
    "action_csv_refund_observe",
    { action_id: prepared.action_id },
    "alice",
    options,
  );
  assert.deepEqual(observed, current);
  if (application) {
    for (const host of [application.node, application.python]) {
      // The old unknown entry now resolves to an observation, without new IDs.
      const fresh = await host.followUp(refundEntry, "alice", [
        "imports:read",
        "payments:read",
      ]);
      assert.equal(fresh.isError, false, JSON.stringify(fresh.content));
      assert.deepEqual(fresh.structuredContent, {
        kind: "tool_call",
        state: "succeeded",
        name: "action_csv_refund_observe",
        arguments: { action_id: prepared.action_id },
      });
      const output = await host.call(
        fresh.structuredContent.name,
        fresh.structuredContent.arguments,
        "alice",
      );
      assert.deepEqual(output.structuredContent, observed.structuredContent);
    }
  }
  assert.equal(fixture.posts(), 1);
  assert.equal(
    (
      await pool.query(
        "SELECT count(*)::int AS n FROM gregale_outbound_actions",
      )
    ).rows[0].n,
    1,
  );
  assert.equal(
    (await pool.query("SELECT count(*)::int AS n FROM sdk_csv_rows")).rows[0].n,
    0,
  );
}
