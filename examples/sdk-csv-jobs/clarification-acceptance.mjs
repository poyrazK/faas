import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";
import { runWorkflowInvocation } from "../../sdk/node/dist/workflows.js";
import { clarificationFamily, clarificationTool } from "./clarification.mjs";

export async function clarificationAcceptance({
  application,
  sdk,
  clarification,
  revisedClarification,
  replaceMappingSource,
  importId,
  pool,
  storage,
}) {
  const { node, python: originalPython } = application;
  const progress = (result) => result._meta["io.gregale/pipeline"];
  const events = async (runId) =>
    (
      await pool.query("SELECT body FROM sdk_pipeline_events WHERE run_id=$1", [
        runId,
      ])
    ).rows;

  async function advance(runId, pipeline = clarification.pipeline, failure) {
    const { record, journal } = (
      await pool.query(
        "SELECT record,journal FROM sdk_pipeline_runs WHERE id=$1",
        [runId],
      )
    ).rows[0];
    for (let invocation = journal.length + 1; invocation <= 80; invocation++) {
      const reply = await runWorkflowInvocation(
        pipeline.definition,
        {
          protocol: "gregale.workflow.code/v1",
          run_id: runId,
          workflow: record.workflow_name,
          input: record.input,
          invocation,
          journal,
        },
      );
      if (reply.result.status === "failed") {
        assert.equal(reply.result.error, failure && "pipeline:" + failure, JSON.stringify(reply));
        record.status = "failed";
        record.last_error = reply.result.error;
        await pool.query("UPDATE sdk_pipeline_runs SET record=$2 WHERE id=$1", [runId, record]);
        return;
      }
      record.status = "running";
      for (const op of reply.ops) {
        if (op.kind === "event") {
          const [event] = await events(runId);
          if (!event) {
            record.status = "awaiting_event";
            break;
          }
          journal[op.seq] = {
            ...op,
            status: "succeeded",
            attempt: 1,
            output: event.body.payload,
          };
        } else {
          assert.notEqual(op.status, "failed", JSON.stringify(op));
          journal[op.seq] = {
            ...op,
            status: op.kind === "sleep" ? "succeeded" : op.status,
            attempt: 1,
          };
        }
      }
      if (reply.result.status === "succeeded") {
        assert.equal(failure, undefined);
        record.status = "succeeded";
        record.output = reply.result.output;
      }
      await pool.query(
        "UPDATE sdk_pipeline_runs SET record=$2,journal=$3 WHERE id=$1",
        [runId, record, JSON.stringify(journal)],
      );
      if (record.status !== "running") return;
      assert.ok(reply.ops.length, JSON.stringify(reply));
      if (reply.ops.some((op) => op.kind === "sleep")) await delay(100);
    }
    assert.fail(
      "CSV clarification workflow did not reach the gate or complete",
    );
  }

  function snapshot(response) {
    assert.equal(response.data.error, undefined, JSON.stringify(response));
    assert.equal(
      response.data.result.status,
      "input_required",
      JSON.stringify(response),
    );
    return response.data.result;
  }

  function denied(response, code) {
    assert.equal(response.data.result, undefined, JSON.stringify(response));
    assert.equal(response.data.error.code, code, JSON.stringify(response));
  }

  async function start(host, waitForInput = true, sourceImport = importId) {
    const handle = await host.startTask(clarificationTool + "_start", {
      request_id: randomUUID(),
      input: {
        import_id: sourceImport,
        contact: "contact@example.test",
        alternate_contact: "alternate@example.test",
      },
    });
    assert.equal(handle.resultType, "task");
    const taskId = handle.taskId;
    const runId = progress(await host.task(taskId)).run_id;
    if (waitForInput) await advance(runId);
    return { taskId, runId };
  }

  async function acknowledged(stream, ids) {
    const ack = await stream.next();
    assert.equal(ack?.method, "notifications/subscriptions/acknowledged");
    assert.deepEqual(ack.params.notifications.taskIds, ids);
  }

  async function update(stream, taskId) {
    const frame = await stream.next();
    assert.equal(frame?.method, "notifications/tasks", JSON.stringify(frame));
    assert.equal(frame.params.taskId, taskId);
    assert.equal(
      frame.params._meta["io.modelcontextprotocol/subscriptionId"],
      1,
    );
    return frame.params;
  }

  async function quiet(nextUpdate) {
    const noUpdate = Symbol();
    assert.equal(
      await Promise.race([nextUpdate, delay(2600, noUpdate)]),
      noUpdate,
    );
  }

  const bindings = await application.prepareTaskBindings(clarificationTool);
  assert.deepEqual(bindings.artifactNames, ["mapping_report"]);
  const input = {
    import_id: importId,
    contact: "contact@example.test",
    alternate_contact: "alternate@example.test",
  };
  const methods = [];
  const taskClient = bindings.createClient(node.taskTransport(methods));
  const checkpoint = JSON.parse(
    JSON.stringify(
      await taskClient.start(input, randomUUID(), { timeoutMs: 10000 }),
    ),
  );
  const taskId = checkpoint.task_id;
  const reader = taskClient.resume(checkpoint);
  const sdkWatch = reader.watch({ timeoutMs: 15000 });
  const live = await node.watchTask(taskId);
  let streamed, runId;
  try {
    await acknowledged(live, [taskId]);
    const initial = await update(live, taskId);
    assert.equal(initial.status, "working");
    assert.equal(progress(initial).state, "running");
    const sdkInitial = (await sdkWatch.next()).value;
    assert.equal(sdkInitial.status, "working");
    runId = sdkInitial.progress.run_id;
    assert.equal(runId, progress(initial).run_id);
    await advance(runId);
    let sdkForm;
    do {
      sdkForm = (await sdkWatch.next()).value;
    } while (sdkForm.status === "working");
    assert.equal(sdkForm.status, "input_required");
    do {
      streamed = await update(live, taskId);
    } while (streamed.status === "working");
    assert.equal(streamed.status, "input_required");
    assert.equal(progress(streamed).state, "awaiting_input");
    assert.deepEqual(sdkForm.forms, streamed.inputRequests);
    await sdkWatch.return();
    const next = live.next();
    try {
      await quiet(next);
    } finally {
      // An interrupted stream can produce an AbortError when the socket closes.
      next.catch(() => {});
    }
  } finally {
    live.close();
    await sdkWatch.return();
  }
  assert.deepEqual(methods, ["tools/call", "subscriptions/listen"]);
  const upgraded = await application.publishTaskRevision();
  const { host: python } = upgraded;
  const revised = await upgraded.prepareBindings();
  assert.deepEqual(
    revised.manifest.pipelines
      .filter(({ revision }) => revision?.family === clarificationFamily)
      .map(({ tool, revision, contract_sha256 }) => [tool, revision.starts, contract_sha256]),
    [
      [clarificationTool, "retained", checkpoint.contract_sha256],
      ["clarify_csv_columns_v2", "default", revisedClarification.contractHash],
    ],
  );
  assert.deepEqual(revised.artifactNames, ["mapping_report_v2"]);
  const options = { timeoutMs: 10000 };
  const previous = bindings.createClient(python.taskTransport());
  const current = revised.createClient(python.taskTransport());
  assert.throws(() => current.resume(checkpoint), { code: "invalid_input" });
  assert.deepEqual(await previous.start(input, checkpoint.request_id, options), checkpoint);
  await assert.rejects(previous.start(input, randomUUID(), options), { code: "revision_unavailable" });
  const errors = async (tool, args, code, subject) => {
    const response = await python.startTask(tool + "_start", args, subject);
    assert.equal(response.isError, true, JSON.stringify(response));
    assert.deepEqual(JSON.parse(response.content[0].text), { code });
  };
  const upgradedInput = { ...input, source_system: "crm" };
  const revisedRequest = randomUUID();
  await errors("clarify_csv_columns_v2", {
    request_id: revisedRequest, input: upgradedInput, expected_contract_sha256: checkpoint.contract_sha256,
  }, "revision_unavailable");
  await errors(clarificationTool, {
    request_id: checkpoint.request_id, input: { ...input, contact: "changed@example.test" },
  }, "receipt_conflict");
  await errors(clarificationTool, {
    request_id: checkpoint.request_id, input,
  }, "unauthorized", "bob");

  const previewArgs = { input: upgradedInput, request_id: revisedRequest };
  await assert.rejects(current.start(upgradedInput, revisedRequest, options), { code: "preview_required" });
  const firstPreview = await current.preview(upgradedInput, revisedRequest, revised.previewContract, options);
  assert.deepEqual(firstPreview.summary.email_columns, ["contact", "alternate_contact"]);
  assert.equal(firstPreview.summary.requires_clarification, true);
  const deniedPreview = await python.call("clarify_csv_columns_v2_preview", previewArgs, "bob");
  assert.equal(deniedPreview.isError, true);
  assert.deepEqual(JSON.parse(deniedPreview.content[0].text), { code: "unauthorized" });

  const reviewedSource = await replaceMappingSource("contact\nreviewed@example.test\n");
  await assert.rejects(
    current.start(upgradedInput, revisedRequest, { ...options, previewToken: firstPreview.preview_token }),
    { code: "preview_stale" },
  );
  const refreshed = await upgraded.consume("preview", previewArgs, revised.pythonBinding);
  assert.notEqual(refreshed.summary.source_sha256, firstPreview.summary.source_sha256);
  assert.deepEqual(refreshed.summary.email_columns, ["contact"]);
  const issuedByNode = await revisedClarification.adapter.call(
    "clarify_csv_columns_v2_preview", previewArgs, "alice", options,
  );
  assert.deepEqual(issuedByNode.structuredContent.summary, refreshed.summary);
  const transport = python.taskTransport();
  const lostReply = revised.createClient({
    async request(method, params, settings) {
      const accepted = await transport.request(method, params, settings);
      assert.equal(accepted.resultType, "task", JSON.stringify(accepted));
      throw Error("lost response after acceptance");
    },
  });
  await assert.rejects(lostReply.start(upgradedInput, revisedRequest, {
    ...options, previewToken: issuedByNode.structuredContent.preview_token,
  }), { code: "request_failed" });
  const accepted = await upgraded.consume("start",
    { ...previewArgs, preview_token: issuedByNode.structuredContent.preview_token }, revised.pythonBinding);
  assert.deepEqual(accepted.methods, ["tools/call"]);
  const fresh = accepted.checkpoint;
  assert.equal(fresh.tool, "clarify_csv_columns_v2");
  assert.notEqual(fresh.contract_sha256, checkpoint.contract_sha256);
  assert.notEqual(fresh.task_id, taskId);
  assert.deepEqual(await current.start(upgradedInput, revisedRequest, options), fresh);
  const nextRunId = progress(await python.task(fresh.task_id)).run_id;
  assert.notEqual(nextRunId, runId);
  const nativeStarts = await pool.query(
    "SELECT record->>'workflow_name' AS name, count(*)::int AS count FROM sdk_pipeline_runs "
      + "WHERE record->>'workflow_name' IN ('clarify-csv-columns', 'clarify-csv-columns-v2') GROUP BY 1 ORDER BY 1",
  );
  assert.deepEqual(nativeStarts.rows, [
    { name: "clarify-csv-columns", count: 1 },
    { name: "clarify-csv-columns-v2", count: 1 },
  ]);
  await advance(nextRunId, revisedClarification.pipeline);
  const freshSnapshot = snapshot(await python.inputTask(fresh.task_id));
  const [[freshKey, freshForm]] = Object.entries(freshSnapshot.inputRequests);
  assert.deepEqual(freshForm.params.requestedSchema.properties.email_column.enum, ["contact"]);
  const replacement = await replaceMappingSource("contact\nchanged@example.test\n");
  assert.notEqual(replacement.version_id, reviewedSource.version_id);
  const { record: pinnedRun } = (await pool.query(
    "SELECT record FROM sdk_pipeline_runs WHERE id=$1", [nextRunId],
  )).rows[0];
  assert.deepEqual(pinnedRun.input.pinned_source, reviewedSource);
  assert.deepEqual(await current.start(upgradedInput, revisedRequest, options), fresh);

  const acceptedReply = await python.updateTask(fresh.task_id, {
    [freshKey]: { action: "accept", content: { email_column: "contact" } },
  });
  assert.equal(acceptedReply.data.error, undefined, JSON.stringify(acceptedReply));
  await advance(nextRunId, revisedClarification.pipeline);
  const pinnedReader = current.resume(fresh);
  const mapped = await pinnedReader.get(options);
  assert.equal(mapped.status, "completed");
  assert.equal(mapped.output.email, "reviewed@example.test");
  const pinnedReport = await pinnedReader.readArtifact(mapped, "mapping_report_v2", options);
  assert.equal(pinnedReport.text, `import_id,email_column,email\n${importId},contact,reviewed@example.test\n`);

  const first = snapshot(await node.inputTask(taskId));
  assert.deepEqual(streamed.inputRequests, first.inputRequests);
  const reconnected = snapshot(await python.inputTask(taskId));
  assert.deepEqual(reconnected.inputRequests, first.inputRequests);
  const [[key, form]] = Object.entries(first.inputRequests);
  assert.equal(form.method, "elicitation/create");
  assert.equal(form.params.mode, "form");
  assert.deepEqual(form.params.requestedSchema.properties.email_column.enum, [
    "contact",
    "alternate_contact",
  ]);
  const respond = (value, id = key) => ({
    [id]: { action: "accept", content: { email_column: value } },
  });
  const missingReplyScope = ["imports:read", "imports:recover"];
  for (const [host, hidden] of [
    [node, -32602],
    [python, -32002],
  ]) {
    const foreign = await host.watchTask(taskId, "bob");
    try {
      await acknowledged(foreign, []);
      assert.equal((await foreign.next()).result.resultType, "complete");
      assert.equal(await foreign.next(), null);
    } finally {
      foreign.close();
    }
    const fallback = await host.task(taskId);
    assert.equal(fallback.status, "working");
    assert.equal(fallback.inputRequests, undefined);
    const link = progress(fallback).review.review_url;
    assert.equal(
      (await fetch(link, { headers: { authorization: "Bearer bob-review" } }))
        .status,
      404,
    );
    const review = await fetch(link, {
      headers: { authorization: "Bearer alice-review" },
    });
    assert.deepEqual((await review.json()).form, form.params);
    assert.equal(
      (await host.inputTask(taskId, "alice", missingReplyScope)).data.result
        .status,
      "working",
    );
    denied(await host.inputTask(taskId, "bob"), hidden);
    denied(await host.updateTask(taskId, respond("contact"), "bob"), hidden);
    denied(
      await host.updateTask(
        taskId,
        respond("contact"),
        "alice",
        missingReplyScope,
      ),
      hidden,
    );
    denied(await host.updateTask(taskId, respond("unknown_column")), -32602);
  }

  // Both hosts reauthorize subscriptions that have already shown a snapshot.
  const reopened = await Promise.all([
    node.watchTask(taskId),
    python.watchTask(taskId, "alice", false),
  ]);
  try {
    await Promise.all(reopened.map((stream) => acknowledged(stream, [taskId])));
    const [withForm, withLink] = await Promise.all(
      reopened.map((stream) => update(stream, taskId)),
    );
    assert.deepEqual(withForm.inputRequests, first.inputRequests);
    assert.equal(withLink.status, "working");
    assert.equal(withLink.inputRequests, undefined);
    assert.equal(
      progress(withLink).review.review_url,
      progress(first).review.review_url,
    );
    const next = Promise.all(reopened.map((stream) => stream.next()));
    await quiet(next);
    await pool.query(
      "UPDATE sdk_csv_query_imports SET enabled=false WHERE import_id=$1",
      [importId],
    );
    for (const message of await next)
      assert.equal(message.result.resultType, "complete");
    for (const stream of reopened) assert.equal(await stream.next(), null);
    denied(await node.updateTask(taskId, respond("contact")), -32602);
    denied(await python.updateTask(taskId, respond("contact")), -32002);
  } finally {
    for (const stream of reopened) stream.close();
    await pool.query(
      "UPDATE sdk_csv_query_imports SET enabled=true WHERE import_id=$1",
      [importId],
    );
  }
  assert.deepEqual(await events(runId), []);
  const answeredFromPython = await upgraded.consume(
    "answer",
    { checkpoint },
    bindings.pythonBinding,
  );
  assert.equal(answeredFromPython.task_id, taskId);
  assert.deepEqual(answeredFromPython.forms, first.inputRequests);
  assert.deepEqual(answeredFromPython.methods, [
    "subscriptions/listen",
    "tasks/update",
  ]);
  assert.equal((await events(runId)).length, 1);
  const duplicate = await Promise.all([
    node.updateTask(taskId, respond("alternate_contact")),
    python.updateTask(taskId, respond("alternate_contact")),
  ]);
  for (const response of duplicate) {
    assert.equal(response.data.error, undefined, JSON.stringify(response));
    assert.equal(response.data.result.resultType, "complete");
  }
  await advance(runId);
  let streamedReport;
  const resumed = await python.watchTask(taskId);
  try {
    await acknowledged(resumed, [taskId]);
    const terminal = await update(resumed, taskId);
    assert.equal(terminal.status, "completed");
    assert.equal(progress(terminal).state, "succeeded");
    assert.equal(terminal.inputRequests, undefined);
    const { state, email } = terminal.result.structuredContent.output;
    assert.deepEqual(
      { state, email },
      { state: "mapped", email: "alternate@example.test" },
    );
    streamedReport = terminal.result.content.find(
      (block) => block.type === "resource_link",
    );
  } finally {
    resumed.close();
  }

  for (const host of [node, python]) {
    const done = await host.task(taskId);
    assert.equal(done.status, "completed");
    const { state, email, report } = done.result.structuredContent.output;
    assert.deepEqual(
      { state, email },
      { state: "mapped", email: "alternate@example.test" },
    );
    assert.equal(report.owner_id, "alice");
    const link = done.result.content.find(
      (block) => block.type === "resource_link",
    );
    assert.deepEqual(link, streamedReport);
    const requestId = progress(done).request_id;
    const direct = await host.call(
      clarificationTool + "_result",
      { request_id: requestId },
      "alice",
    );
    assert.deepEqual(
      direct.content.find((block) => block.type === "resource_link"),
      link,
    );
    const withoutReports = await host.result(
      "tools/call",
      {
        name: clarificationTool + "_result",
        arguments: { request_id: requestId },
      },
      "alice",
      ["imports:read"],
    );
    assert.ok(
      !withoutReports.content.some((block) => block.type === "resource_link"),
    );
    assert.equal(
      (await host.updateTask(taskId, respond("alternate_contact"))).data.error,
      undefined,
    );
    denied(await host.updateTask(taskId, respond("contact")), -32602);
  }
  assert.equal((await events(runId)).length, 1);

  const { uri, mimeType } = streamedReport;
  assert.equal(mimeType, "text/csv");
  assert.match(
    uri,
    /^gregale-pipeline:\/\/[a-f0-9]{64}\/mapping_report\/[A-Za-z0-9_-]{64}$/,
  );
  const report = (await node.task(taskId)).result.structuredContent.output
    .report;
  const original = storage.objects.get(report.key);
  const expected = {
    contents: [
      {
        uri,
        mimeType,
        text: `import_id,email_column\n${importId},alternate_contact\n`,
      },
    ],
  };
  const readFromPython = await upgraded.consume(
    "read",
    { checkpoint },
    bindings.pythonBinding,
  );
  assert.equal(readFromPython.output.email, "alternate@example.test");
  assert.deepEqual(readFromPython.report, expected.contents[0]);
  assert.deepEqual(readFromPython.methods, [
    "subscriptions/listen",
    "resources/read",
  ]);
  for (const [host, hidden] of [
    [node, -32602],
    [python, -32002],
  ]) {
    for (let read = 0; read < 2; read++)
      assert.deepEqual(await host.result("resources/read", { uri }), expected);
    const issued = storage.counts().issued;
    denied(await host.rpc("resources/read", { uri }, "bob"), hidden);
    const forged = uri.slice(0, -1) + (uri.endsWith("A") ? "B" : "A");
    denied(await host.rpc("resources/read", { uri: forged }), hidden);
    const missingScope = await host.rpc("resources/read", { uri }, "alice", [
      "imports:read",
    ]);
    assert.equal(missingScope.status, 403);
    assert.equal(missingScope.data.result, undefined);
    assert.equal(storage.counts().issued, issued);

    storage.setDownloadHook(async (key) => {
      if (key === report.key)
        await pool.query(
          "UPDATE sdk_csv_query_imports SET enabled=false WHERE import_id=$1",
          [importId],
        );
    });
    try {
      denied(await host.rpc("resources/read", { uri }), hidden);
      denied(await host.rpc("resources/read", { uri }), hidden);
    } finally {
      storage.setDownloadHook(undefined);
      await pool.query(
        "UPDATE sdk_csv_query_imports SET enabled=true WHERE import_id=$1",
        [importId],
      );
    }

    try {
      const changed = Buffer.from(original);
      changed[0] ^= 1;
      storage.objects.set(report.key, changed);
      denied(await host.rpc("resources/read", { uri }), -32603);
      storage.objects.set(report.key, Buffer.alloc(65537, 65));
      denied(await host.rpc("resources/read", { uri }), -32603);
    } finally {
      storage.objects.set(report.key, original);
    }
    assert.deepEqual(await host.result("resources/read", { uri }), expected);
  }

  const { record } = (
    await pool.query("SELECT record FROM sdk_pipeline_runs WHERE id=$1", [
      runId,
    ])
  ).rows[0];
  const oversized = structuredClone(record);
  oversized.output.output.report.size_bytes = 65537;
  await pool.query("UPDATE sdk_pipeline_runs SET record=$2 WHERE id=$1", [
    runId,
    oversized,
  ]);
  try {
    const issued = storage.counts().issued;
    for (const [host, hidden] of [
      [node, -32602],
      [python, -32002],
    ]) {
      const oversizedUri = (await host.task(taskId)).result.content.find(
        (block) => block.type === "resource_link",
      ).uri;
      assert.notEqual(oversizedUri, uri);
      denied(await host.rpc("resources/read", { uri }), hidden);
      denied(await host.rpc("resources/read", { uri: oversizedUri }), -32603);
    }
    assert.equal(storage.counts().issued, issued);
  } finally {
    await pool.query("UPDATE sdk_pipeline_runs SET record=$2 WHERE id=$1", [
      runId,
      record,
    ]);
  }

  const stale = await start(originalPython);
  const pending = snapshot(await node.inputTask(stale.taskId));
  const [staleKey] = Object.keys(pending.inputRequests);
  assert.notEqual(staleKey, key);
  denied(await python.updateTask(stale.taskId, respond("contact")), -32602);
  await pool.query(
    "UPDATE gregale_pipeline_gates SET expires_at=clock_timestamp() WHERE id=$1",
    [progress(pending).review.id],
  );
  for (const host of [node, python]) {
    denied(
      await host.updateTask(stale.taskId, respond("contact", staleKey)),
      -32602,
    );
    const task = (await host.inputTask(stale.taskId)).data.result;
    assert.equal(task.inputRequests, undefined);
  }
  assert.deepEqual(await events(stale.runId), []);

  const withoutForms = await start(originalPython);
  const fallbackStream = await python.watchTask(
    withoutForms.taskId,
    "alice",
    false,
  );
  try {
    await acknowledged(fallbackStream, [withoutForms.taskId]);
    const pending = await update(fallbackStream, withoutForms.taskId);
    const link = progress(pending).review.review_url;
    assert.equal(pending.status, "working");
    assert.equal(pending.inputRequests, undefined);
    const headers = {
      authorization: "Bearer alice-review",
      "content-type": "application/json",
    };
    const review = await fetch(link, { headers });
    assert.equal(review.status, 200);
    const { binding_sha256 } = await review.json();
    const answered = await fetch(link, {
      method: "POST",
      headers,
      body: JSON.stringify({
        binding_sha256,
        content: { email_column: "contact" },
      }),
    });
    assert.equal(answered.status, 200);
    await advance(withoutForms.runId);
    let completed;
    do {
      completed = await update(fallbackStream, withoutForms.taskId);
    } while (completed.status === "working");
    assert.equal(completed.status, "completed");
    const { state, email } = completed.result.structuredContent.output;
    assert.deepEqual(
      { state, email },
      { state: "mapped", email: "contact@example.test" },
    );
    assert.deepEqual(
      completed.result.structuredContent,
      (await node.task(withoutForms.taskId)).result.structuredContent,
    );
  } finally {
    fallbackStream.close();
  }
  for (const failure of ["source_unavailable", "source_forbidden"]) {
    const source = await replaceMappingSource("contact\nauthorized-before-pause@example.test\n");
    const requestId = randomUUID();
    const confirmation = await current.preview(upgradedInput, requestId, revised.previewContract, options);
    const checkpoint = await current.start(upgradedInput, requestId, {
      ...options, previewToken: confirmation.preview_token,
    });
    const nativeId = progress(await python.task(checkpoint.task_id)).run_id;
    await advance(nativeId, revisedClarification.pipeline);
    const [key] = Object.keys(snapshot(await python.inputTask(checkpoint.task_id)).inputRequests);
    const answered = await python.updateTask(checkpoint.task_id, {
      [key]: { action: "accept", content: { email_column: "contact" } },
    });
    assert.equal(answered.data.error, undefined, JSON.stringify(answered));
    if (failure === "source_unavailable")
      storage.removeVersion(source.version_id);
    else
      await pool.query("UPDATE sdk_csv_mapping_sources SET enabled=false WHERE import_id=$1", [importId]);
    try {
      await advance(nativeId, revisedClarification.pipeline, failure);
      const observed = await current.resume(checkpoint).get(options);
      assert.equal(observed.status, "failed");
      assert.equal(observed.progress.source_failure, failure);
      const fromPython = await upgraded.consume("observe", { checkpoint }, revised.pythonBinding);
      assert.equal(fromPython.status, "failed");
      assert.equal(fromPython.progress.source_failure, failure);
      assert.equal(storage.objects.has(`pipelines/${importId}/${nativeId}-mapping.csv`), false);

      const recoveryId = randomUUID();
      const fromNode = revised.createClient(upgraded.node.taskTransport());
      const failed = fromNode.resume(checkpoint);
      if (failure === "source_forbidden") {
        await assert.rejects(failed.previewRecovery(recoveryId, revised.previewContract, options), {
          code: "recovery_blocked",
        });
        continue;
      }

      await replaceMappingSource("contact\nreplacement-before-review@example.test\n");
      const before = Number((await pool.query("SELECT count(*) FROM sdk_pipeline_runs")).rows[0].count);
      const proposal = await failed.previewRecovery(recoveryId, revised.previewContract, options);
      assert.equal(proposal.replaces_task_id, checkpoint.task_id);
      assert.equal(proposal.previous_source_sha256, source.sha256);
      assert.notEqual(proposal.summary.source_sha256, source.sha256);
      const hidden = await python.call("pipeline_task_recovery_preview", {
        task_id: checkpoint.task_id,
        recovery_request_id: recoveryId,
        expected_contract_sha256: checkpoint.contract_sha256,
      }, "bob");
      assert.deepEqual(JSON.parse(hidden.content[0].text), { code: "not_found" });
      await pool.query("UPDATE sdk_csv_mapping_sources SET enabled=false WHERE import_id=$1", [importId]);
      try {
        await assert.rejects(failed.confirmRecovery(recoveryId, proposal.preview_token, options), {
          code: "recovery_blocked",
        });
      } finally {
        await pool.query("UPDATE sdk_csv_mapping_sources SET enabled=true WHERE import_id=$1", [importId]);
      }
      await replaceMappingSource("contact\nrecovered@example.test\n");
      await assert.rejects(failed.confirmRecovery(recoveryId, proposal.preview_token, options), {
        code: "preview_stale",
      });
      assert.equal(Number((await pool.query("SELECT count(*) FROM sdk_pipeline_runs")).rows[0].count), before);
      const updated = await failed.previewRecovery(recoveryId, revised.previewContract, options);
      assert.notEqual(updated.summary.source_sha256, proposal.summary.source_sha256);
      const pythonPreview = await upgraded.consume("preview-recovery", {
        checkpoint, recovery_request_id: recoveryId,
      }, revised.pythonBinding);
      assert.deepEqual(pythonPreview.summary, updated.summary);
      assert.equal(pythonPreview.previous_source_sha256, source.sha256);
      const ordinary = await python.call("pipeline_task_recovery_confirm", {
        task_id: checkpoint.task_id, recovery_request_id: recoveryId,
        expected_contract_sha256: checkpoint.contract_sha256, preview_token: updated.preview_token,
      }, "alice");
      assert.deepEqual(JSON.parse(ordinary.content[0].text), { code: "task_mode_required" });
      assert.equal(Number((await pool.query("SELECT count(*) FROM sdk_pipeline_runs")).rows[0].count), before);

      const hostTransport = upgraded.node.taskTransport();
      const losing = revised.createClient({
        async request(method, params, settings) {
          const result = await hostTransport.request(method, params, settings);
          assert.equal(result.resultType, "task", JSON.stringify(result));
          throw Error("lost recovery confirmation response");
        },
      });
      await assert.rejects(losing.resume(checkpoint).confirmRecovery(recoveryId, updated.preview_token, options), {
        code: "request_failed",
      });
      const recovered = await upgraded.consume("confirm-recovery", {
        checkpoint, recovery_request_id: recoveryId, preview_token: updated.preview_token,
      }, revised.pythonBinding);
      assert.notEqual(recovered.task_id, checkpoint.task_id);
      assert.deepEqual(await failed.confirmRecovery(recoveryId, updated.preview_token, options), recovered);
      assert.equal(Number((await pool.query("SELECT count(*) FROM sdk_pipeline_runs")).rows[0].count), before + 1);
      const reader = current.resume(recovered);
      const replacementView = await reader.get(options);
      assert.equal(replacementView.progress.replaces_task_id, checkpoint.task_id);
      const recoveryRunId = replacementView.progress.run_id;
      await advance(recoveryRunId, revisedClarification.pipeline);
      const [reviewKey] = Object.keys(snapshot(await python.inputTask(recovered.task_id)).inputRequests);
      const answeredRecovery = await python.updateTask(recovered.task_id, {
        [reviewKey]: { action: "accept", content: { email_column: "contact" } },
      });
      assert.equal(answeredRecovery.data.error, undefined, JSON.stringify(answeredRecovery));
      await advance(recoveryRunId, revisedClarification.pipeline);
      const outcome = await reader.get(options);
      assert.equal(outcome.status, "completed");
      assert.equal(outcome.progress.replaces_task_id, checkpoint.task_id);
      const report = await reader.readArtifact(outcome, "mapping_report_v2", options);
      assert.equal(report.text, `import_id,email_column,email\n${importId},contact,recovered@example.test\n`);
      assert.equal((await failed.get(options)).status, "failed");
    } finally {
      await pool.query("UPDATE sdk_csv_mapping_sources SET enabled=true WHERE import_id=$1", [importId]);
    }
  }

  const assignment = await start(node);
  const ownerView = snapshot(await node.inputTask(assignment.taskId));
  const [ownerKey] = Object.keys(ownerView.inputRequests);
  const reviewerSubject = "reviewer";
  const scopes = ["imports:handoff", "imports:clarify"];
  await pool.query("INSERT INTO sdk_csv_clarification_reviewers (import_id,actor_id) VALUES ($1,$2)", [importId, reviewerSubject]);
  const reviewer = (handoff_id, subject = reviewerSubject) => sdk.createPipelineTaskHandoffClient({
    handoffId: handoff_id,
    replyContract: bindings.replyContract,
    transport: node.taskTransport([], scopes, subject, "reviewer-node"),
  });
  const reviewerArgs = { subject: reviewerSubject, scopes, clientId: "reviewer-python" };
  const assigned = await application.grantHandoff(assignment.taskId, reviewerSubject);
  const otherImport = randomUUID();
  await pool.query(
    "INSERT INTO sdk_csv_query_imports (import_id,owner_id,source_run_id,failure_report,report_sha256) "
      + "SELECT $2,owner_id,source_run_id,failure_report,report_sha256 FROM sdk_csv_query_imports WHERE import_id=$1",
    [importId, otherImport],
  );
  await pool.query(
    "INSERT INTO sdk_csv_clarification_reviewers (import_id,actor_id) VALUES ($1,$2),($1,'bob')",
    [otherImport, reviewerSubject],
  );
  const other = await start(node, true, otherImport);
  const nextGrant = await application.grantHandoff(other.taskId, reviewerSubject);
  const foreign = await application.grantHandoff(other.taskId, "bob");
  const inboxRequests = [];
  const inbox = sdk.createPipelineTaskReviewerInbox(node.taskTransport(inboxRequests, scopes, reviewerSubject, "reviewer-node"));
  const bobInbox = sdk.createPipelineTaskReviewerInbox(node.taskTransport([], scopes, "bob", "bob-reviewer"));
  const ids = (view) => view.items.map((item) => item.handoff_id);
  const reviewerAssignments = [assigned.handoff_id, nextGrant.handoff_id].sort();
  const firstPage = await inbox.page({ ...options, pageSize: 1 });
  assert.equal(firstPage.items.length, 1);
  assert.deepEqual(Object.keys(firstPage.items[0]).sort(), ["expires_at", "handoff_id", "question"]);
  assert.match(firstPage.items[0].question, /email address/);
  assert.ok(firstPage.next_cursor);
  const nextPage = await upgraded.consume("reviewer-inbox-page", {
    ...reviewerArgs, page_size: 1, cursor: firstPage.next_cursor,
  }, bindings.pythonBinding);
  assert.equal(nextPage.next_cursor, null);
  assert.deepEqual([...ids(firstPage), ...ids(nextPage)].sort(), reviewerAssignments);
  assert.deepEqual(ids(await bobInbox.page(options)), [foreign.handoff_id]);
  await assert.rejects(bobInbox.page({ ...options, cursor: firstPage.next_cursor }), { code: "invalid_input" });

  const resourceId = "csv-reviewer-inbox";
  let sink;
  const watching = inbox.watch({
    timeoutMs: 60000, pageSize: 1, refreshIntervalMs: 30000,
    notifications: { resourceId, source(observer) { sink = observer; sink.ready(); return () => {}; } },
  });
  const membership = (enabled) => pool.query(
    "UPDATE sdk_csv_clarification_reviewers SET enabled=$3 WHERE import_id=$1 AND actor_id=$2",
    [importId, reviewerSubject, enabled],
  );
  try {
    assert.deepEqual(ids((await watching.next()).value).sort(), reviewerAssignments);
    await membership(false);
    const update = sdk.workInboxInvalidation(resourceId, "membership-revoked");
    sink.changed(update);
    sink.changed(update);
    assert.deepEqual(ids((await watching.next()).value), [nextGrant.handoff_id]);
    const sampled = inboxRequests.length;
    sink.changed(update);
    const refreshed = watching.next();
    await delay(40);
    assert.equal(inboxRequests.length, sampled);
    sink.disconnected();
    await application.revokeHandoff(nextGrant.handoff_id);
    await membership(true);
    sink.ready();
    const reconnected = (await refreshed).value;
    assert.deepEqual(ids(reconnected), [assigned.handoff_id]);
    assert.deepEqual(await upgraded.consume("reviewer-inbox-watch", {
      ...reviewerArgs, page_size: 1,
    }, bindings.pythonBinding), reconnected);
  } finally {
    await watching.return();
  }
  assert.deepEqual(ids(await bobInbox.page(options)), [foreign.handoff_id]);

  const read = await reviewer(assigned.handoff_id).get(options);
  assert.equal(read.expires_at, assigned.expires_at);
  assert.ok(Date.parse(read.expires_at) <= Date.parse(progress(ownerView).review.expires_at));
  assert.deepEqual(Object.keys(read).sort(), ["expires_at", "form", "handoff_id", "request_key"]);
  const crossHost = await upgraded.consume("handoff-read", {
    ...reviewerArgs, handoff_id: assigned.handoff_id,
  }, bindings.pythonBinding);
  assert.deepEqual(crossHost, read);
  assert.deepEqual(crossHost.form, ownerView.inputRequests[ownerKey]);
  assert.notEqual(read.request_key, ownerKey);

  const answerValue = { email_column: "alternate_contact" };
  await assert.rejects(reviewer(assigned.handoff_id, "bob").get(options), { code: "not_found" });
  await assert.rejects(reviewer(assigned.handoff_id, "bob").answer(read, answerValue, options), { code: "not_found" });
  await assert.rejects(reviewer(assigned.handoff_id).answer({ ...read, request_key: ownerKey }, answerValue, options), {
    code: "invalid_input",
  });
  denied(await python.inputTask(assignment.taskId, reviewerSubject, scopes), -32002);

  const expired = await application.grantHandoff(assignment.taskId, reviewerSubject);
  const expiredReader = reviewer(expired.handoff_id), expiredForm = await expiredReader.get(options);
  await pool.query("UPDATE gregale_pipeline_task_handoffs SET expires_at=clock_timestamp() WHERE id=$1", [expired.handoff_id]);
  await assert.rejects(expiredReader.answer(expiredForm, answerValue, options), { code: "not_found" });

  const revoked = await application.grantHandoff(assignment.taskId, reviewerSubject);
  const revokedReader = reviewer(revoked.handoff_id), revokedForm = await revokedReader.get(options);
  await assert.rejects(revokedReader.answer({ ...revokedForm, request_key: read.request_key }, answerValue, options), {
    code: "invalid_input",
  });
  await application.revokeHandoff(revoked.handoff_id);
  await assert.rejects(revokedReader.get(options), { code: "not_found" });
  await assert.rejects(revokedReader.answer(revokedForm, answerValue, options), { code: "not_found" });

  const racing = await application.grantHandoff(assignment.taskId, reviewerSubject);
  const racingReader = reviewer(racing.handoff_id), racingForm = await racingReader.get(options);
  const transaction = await pool.connect();
  try {
    await transaction.query("BEGIN");
    await transaction.query("UPDATE gregale_pipeline_task_handoffs SET revoked_at=clock_timestamp() WHERE id=$1", [racing.handoff_id]);
    const response = racingReader.answer(racingForm, answerValue, options).then(() => null, (error) => error);
    let blocked = false;
    for (let n = 0; n < 100 && !blocked; n++) {
      const row = (await pool.query(
        "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE query LIKE 'WITH live AS MATERIALIZED%' AND wait_event_type='Lock') AS blocked",
      )).rows[0];
      blocked = row.blocked;
      if (!blocked) await delay(20);
    }
    assert.ok(blocked, "The guarded decision waited for the grant lock");
    await transaction.query("COMMIT");
    assert.equal((await response)?.code, "not_found");
  } finally {
    await transaction.query("ROLLBACK");
    transaction.release();
  }

  await pool.query("UPDATE sdk_csv_clarification_reviewers SET enabled=false WHERE import_id=$1 AND actor_id=$2", [importId, reviewerSubject]);
  await assert.rejects(reviewer(assigned.handoff_id).answer(read, answerValue, options), { code: "not_found" });
  await pool.query("UPDATE sdk_csv_clarification_reviewers SET enabled=true WHERE import_id=$1 AND actor_id=$2", [importId, reviewerSubject]);
  assert.deepEqual(await events(assignment.runId), []);

  assert.deepEqual(await upgraded.consume("handoff-answer", {
    ...reviewerArgs, handoff_id: assigned.handoff_id, view: crossHost, input: answerValue,
  }, bindings.pythonBinding), { submitted: true });
  await reviewer(assigned.handoff_id).answer(read, answerValue, options);
  await assert.rejects(reviewer(assigned.handoff_id).answer(read, { email_column: "contact" }, options), {
    code: "invalid_input",
  });
  assert.deepEqual(ids(await inbox.page(options)), []);
  const decision = await clarification.gates.inspect(progress(ownerView).review.id, "alice", options);
  assert.equal(decision.decision.actor_id, reviewerSubject);
  assert.equal((await events(assignment.runId)).length, 1);
  await advance(assignment.runId);
  const outcome = await node.task(assignment.taskId);
  assert.equal(outcome.status, "completed");
  assert.equal(outcome.result.structuredContent.output.email, "alternate@example.test");
  assert.ok(outcome.result.content.some((item) => item.type === "resource_link" && item.name === "mapping_report"));
}
