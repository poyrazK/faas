import assert from "node:assert/strict";
import { generateKeyPairSync, randomUUID, sign } from "node:crypto";
import { once } from "node:events";
import { spawn } from "node:child_process";
import { writeFile } from "node:fs/promises";
import { delimiter, join } from "node:path";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import { csvApplicationToolkit } from "./application-toolkit.mjs";
import { preparePipelineTaskBindings, prepareRevisedTaskBindings } from "./pipeline-task-bindings-acceptance.mjs";

import { clarificationTool } from "./clarification.mjs";

const issuer = "https://issuer.example.test";
export const audience = "https://app.example.test/mcp";
const support = ["imports:read", "reports:read"];
const operator = [
  ...support,
  "imports:recover",
  "imports:clarify",
  "imports:preview",
  "imports:replace",
  "payments:read",
  "payments:refund",
];
export const config = {
  version: 1,
  endpoint: "/mcp",
  transport: "streamable-http",
  mode: "stateless",
  legacy: true,
  allowed_origins: [],
  tasks: { enabled: false },
  auth: {
    mode: "external-oauth",
    issuer,
    resource: audience,
    jwks_url: issuer + "/jwks",
    scopes: ["mcp:connect"],
    prompt_scopes: {},
  },
};

export const ownerClient = "csv-acceptance";
export function claims(privateKey, subject, scopes, clientId = ownerClient) {
  const encode = (value) =>
    Buffer.from(JSON.stringify(value)).toString("base64url");
  const frame =
    encode({ alg: "RS256", kid: "csv-fixture" }) +
    "." +
    encode({
      iss: issuer,
      aud: audience,
      sub: subject,
      client_id: clientId,
      jti: randomUUID(),
      exp: Math.floor(Date.now() / 1000) + 60,
      scope: ["mcp:connect", ...scopes].join(" "),
    });
  return (
    frame +
    "." +
    sign("sha256", Buffer.from(frame), privateKey).toString("base64url")
  );
}

export async function applicationToolkitAcceptance({
  sdk,
  adapters,
  workInbox,
  recovery,
  refund,
  env,
  home,
  cleanup,
  importId,
  pool,
  recoveryReviewURL,
  refundFixture,
  withPipelineTasks,
  clarification,
  retainedClarification,
  revisedClarification,
  clarificationReviewURL,
}) {
  const root = new URL("../../", import.meta.url);
  const nodeRoot =
    process.env.GREGALE_MCP_STARTER_NODE ??
    new URL("cmd/gregale/templates/mcp-node-tasks/", root).pathname;
  const pyRoot = new URL("cmd/gregale/templates/mcp-python-tasks/", root).pathname;
  const { createApp } = await import(pathToFileURL(join(nodeRoot, "app.js")));
  function mountClarification(definition) {
    const { adapter } = definition;
    const tool = adapter.pipelineContract.tool;
    return {
      adapter,
      toolScopes: {
        [tool + "_start"]: ["imports:recover"],
        [tool + "_status"]: ["imports:read"],
        [tool + "_result"]: ["imports:read"],
        ...(adapter.pipelineContract.preview && { [tool + "_preview"]: ["imports:preview"] }),
      },
      resourceScopes: { [adapter.resourceTemplates[0].uriTemplate]: ["imports:read", "reports:read"] },
    };
  }
  const makeToolkit = (clarifications) => csvApplicationToolkit({
    sdk, adapters, workInbox, recovery, refund,
    extraMounts: clarifications.map(mountClarification),
  });
  const toolkit = await makeToolkit(clarification ? [clarification] : []);
  const { privateKey, publicKey } = generateKeyPairSync("rsa", {
    modulusLength: 2048,
  });
  assert.throws(
    () => createApp({ ...config, auth: { mode: "open" } }, { toolkit }),
    /require external-oauth/,
  );
  const taskSettings = withPipelineTasks
    ? {
        namespace: "csv-pipeline-tasks",
        publishManifest: Boolean(clarification),
        ownerKey: randomUUID() + randomUUID(),
        tools: [
          "recover_csv_cleanup",
          ...(clarification ? [clarificationTool] : []),
        ],
        ...(clarification && {
          clarification: { importId, reviewBaseURL: clarificationReviewURL },
        }),
      }
    : undefined;
  if (taskSettings) await pool.query(sdk.pipelineTaskSchema);
  if (clarification) await pool.query(sdk.pipelineTaskHandoffSchema);
  const handoffs = clarification ? {
    scopes: ["imports:handoff"], store: sdk.postgresPipelineTaskHandoffStore((q, v) => pool.query(q, v)),
  } : undefined;
  const owningCaller = {
    resource: audience, subject: "alice", clientId: ownerClient, access: { principal: "alice", scopes: operator },
  };
  const pipelineTasks =
    taskSettings &&
    sdk.createPipelineTasks(toolkit, {
      ...taskSettings,
      handoffs,
      inputForms: clarification
        ? { [clarificationTool]: clarification.input }
        : undefined,
      store: sdk.postgresPipelineTaskStore((q, v) => pool.query(q, v)),
    });
  async function launchNode(toolkit, pipelineTasks) {
    const app = createApp(config, { toolkit, pipelineTasks, keyResolver: publicKey, log() {} });
    const server = app.app.listen(0, "127.0.0.1");
    await once(server, "listening");
    cleanup.push(async () => {
      app.closeTaskSubscriptions();
      await app.handler.close();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    });
    return server.address().port;
  }
  const nodePort = await launchNode(toolkit, pipelineTasks);

  const python =
    process.env.GREGALE_LOCAL_PYTHON ??
    new URL("sdk/python/.venv/bin/python", root).pathname;
  async function launchPython(taskConfig, filename) {
    const settings = join(home, filename);
    await writeFile(
      settings,
      JSON.stringify([
        config,
        {
          ...publicKey.export({ format: "jwk" }),
          alg: "RS256",
          kid: "csv-fixture",
        },
        recoveryReviewURL,
        refundFixture.reviewURL,
        refundFixture.relayURL,
        taskConfig ?? null,
      ]),
    );
    const child = spawn(
      python,
      [
        new URL("./application_toolkit_fixture.py", import.meta.url).pathname,
        pyRoot,
        settings,
      ],
      {
        env: {
          ...env,
          PYTHONPATH: [env.PYTHONPATH, process.env.GREGALE_MCP_STARTER_PYTHONPATH]
            .filter(Boolean)
            .join(delimiter),
        },
        stdio: ["ignore", "pipe", "pipe"],
      },
    );
    let errors = "";
    child.stderr.on("data", (data) => {
      errors = (errors + String(data)).slice(-8000);
    });
    const lines = createInterface({ input: child.stdout });
    cleanup.push(async () => {
      lines.close();
      if (child.exitCode === null && child.signalCode === null) {
        const stopped = once(child, "exit");
        child.kill("SIGTERM");
        await stopped;
      }
    });
    return new Promise((resolve, reject) => {
      const timer = setTimeout(
        () => reject(Error("Python toolkit host did not start: " + errors)),
        120000,
      );
      child.once("error", (err) => {
        clearTimeout(timer);
        reject(err);
      });
      child.once("exit", (code) => {
        clearTimeout(timer);
        reject(Error("Python toolkit host exited " + code + ": " + errors));
      });
      lines.once("line", (line) => {
        clearTimeout(timer);
        try {
          resolve(JSON.parse(line).port);
        } catch (err) {
          reject(err);
        }
      });
    });
  }
  const pyPort = await launchPython(taskSettings, "toolkit-http-fixture.json");
  function client(port) {
    function request(
      method,
      params = {},
      subject = "alice",
      scopes = operator,
      version = "2025-11-25",
      withTasks = false,
      supportsForms = false,
      signal = AbortSignal.timeout(35000),
    ) {
      const name =
        params.name ??
        params.taskId ??
        (method === "resources/read" ? params.uri : undefined);
      const _meta =
        version === "2026-07-28"
          ? {
              "io.modelcontextprotocol/protocolVersion": version,
              "io.modelcontextprotocol/clientInfo": {
                name: "csv-fixture",
                version: "1",
              },
              "io.modelcontextprotocol/clientCapabilities": {
                ...(withTasks && {
                  extensions: { "io.modelcontextprotocol/tasks": {} },
                }),
                ...(supportsForms && { elicitation: { form: {} } }),
              },
            }
          : {};
      return fetch(`http://127.0.0.1:${port}/mcp`, {
        method: "POST",
        headers: {
          Authorization: "Bearer " + claims(privateKey, subject, scopes),
          "Content-Type": "application/json",
          Accept: "application/json, text/event-stream",
          "MCP-Protocol-Version": version,
          "MCP-Method": method,
          ...(name ? { "MCP-Name": name } : {}),
        },
        body: JSON.stringify({
          jsonrpc: "2.0",
          id: 1,
          method,
          params: { ...params, _meta },
        }),
        signal,
      });
    }
    async function rpc(...args) {
      const response = await request(...args);
      const raw = await response.text();
      const json = raw.startsWith("{")
        ? raw
        : raw
            .split("\n")
            .find((line) => line.startsWith("data: "))
            ?.slice(6);
      assert.ok(json, raw);
      return { status: response.status, data: JSON.parse(json) };
    }
    async function result(
      method,
      params,
      subject,
      scopes,
      version,
      withTasks,
      supportsForms,
    ) {
      const { status, data } = await rpc(
        method,
        params,
        subject,
        scopes,
        version,
        withTasks,
        supportsForms,
      );
      assert.equal(status, 200, JSON.stringify(data));
      assert.equal(data.error, undefined, JSON.stringify(data));
      return data.result;
    }
    async function watchTask(taskId, subject = "alice", supportsForms = true) {
      const controller = new AbortController();
      const response = await request(
        "subscriptions/listen",
        { notifications: { taskIds: [taskId] } },
        subject,
        operator,
        "2026-07-28",
        true,
        supportsForms,
        AbortSignal.any([controller.signal, AbortSignal.timeout(30000)]),
      );
      assert.equal(response.status, 200);
      assert.match(response.headers.get("content-type"), /^text\/event-stream/);
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      async function next() {
        while (true) {
          const end = buffer.indexOf("\n\n");
          if (end >= 0) {
            const data = buffer
              .slice(0, end)
              .split("\n")
              .find((line) => line.startsWith("data: "));
            buffer = buffer.slice(end + 2);
            if (data) return JSON.parse(data.slice(6));
            continue;
          }
          const { value, done } = await reader.read();
          if (done) return null;
          buffer += decoder.decode(value, { stream: true });
        }
      }
      return { next, close: () => controller.abort() };
    }
    function taskTransport(methods = [], scopes = operator, subject = "alice", clientId = ownerClient) {
      return sdk.createPipelineTaskHttpTransport({
        endpoint: `http://127.0.0.1:${port}/mcp`,
        fetch(url, init) {
          const headers = new Headers(init.headers);
          methods.push(headers.get("MCP-Method"));
          headers.set(
            "Authorization",
            "Bearer " + claims(privateKey, subject, scopes, clientId),
          );
          return fetch(url, { ...init, headers });
        },
      });
    }
    return {
      rpc,
      result,
      watchTask,
      taskTransport,
      startTask: (name, args, subject = "alice") =>
        result(
          "tools/call",
          { name, arguments: args },
          subject,
          operator,
          "2026-07-28",
          true,
        ),
      task: (taskId, subject = "alice", scopes = operator) =>
        result("tasks/get", { taskId }, subject, scopes, "2026-07-28", true),
      inputTask: (taskId, subject = "alice", scopes = operator) =>
        rpc("tasks/get", { taskId }, subject, scopes, "2026-07-28", true, true),
      updateTask: (
        taskId,
        inputResponses,
        subject = "alice",
        scopes = operator,
      ) =>
        rpc(
          "tasks/update",
          { taskId, inputResponses },
          subject,
          scopes,
          "2026-07-28",
          true,
          true,
        ),
      taskAccess: (taskId, subject, scopes = operator) =>
        rpc("tasks/get", { taskId }, subject, scopes, "2026-07-28", true),
      followUp: ({ kind, name, request_id, business_ref }, subject, scopes) =>
        result(
          "tools/call",
          {
            name: "work_inbox_follow_up",
            arguments: {
              kind,
              name,
              request_id,
              ...(business_ref ? { business_ref } : {}),
            },
          },
          subject,
          scopes,
        ),
      call: (name, args, subject, _options) =>
        result("tools/call", { name, arguments: args }, subject),
    };
  }
  const node = client(nodePort),
    pythonClient = client(pyPort);
  const args = { input: { import_id: importId } };
  for (const host of [node, pythonClient]) {
    const tools = (await host.result("tools/list", {}, "alice", support)).tools
      .map((x) => x.name)
      .sort();
    assert.deepEqual(tools, [
      ...(clarification
        ? [clarificationTool + "_result", clarificationTool + "_status"]
        : []),
      "csv_import_failure",
      "csv_import_rows",
      "recover_csv_cleanup_result",
      "recover_csv_cleanup_status",
    ]);
    const ops = (
      await host.result("tools/list", {}, "alice", operator, "2026-07-28")
    ).tools;
    assert.ok(ops.some((x) => x.name === "action_csv_refund_start"));
    assert.equal(
      ops.find((x) => x.name === "work_inbox_follow_up").annotations
        .readOnlyHint,
      true,
    );
    assert.ok(
      !ops.some(
        (x) => x.name === "action_csv_refund_cancel" || x.name === "greet",
      ),
    );
    const denied = await host.rpc(
      "tools/call",
      { name: "recover_csv_cleanup_start", arguments: {} },
      "alice",
      support,
    );
    assert.equal(denied.status, 403);
    const hidden = await host.rpc("tools/call", {
      name: "action_csv_refund_cancel",
      arguments: {},
    });
    assert.equal(hidden.status, 403);
    const scoped = await host.result(
      "tools/call",
      { name: "csv_import_failure", arguments: args },
      "alice",
      support,
    );
    const uri = scoped.content.find((c) => c.type === "resource_link").uri;
    assert.equal(
      (await host.result("resources/templates/list", {}, "alice", support))
        .resourceTemplates.length,
      clarification ? 2 : 1,
    );
    assert.match(
      (await host.result("resources/read", { uri }, "alice", support))
        .contents[0].text,
      /,report,failed/,
    );
    assert.equal(
      (await host.rpc("resources/read", { uri }, "alice", ["imports:read"]))
        .status,
      403,
    );
    const foreign = await host.rpc("resources/read", { uri }, "bob", support);
    // The Node protocol runtime represents resource-not-found as InvalidParams with URI data.
    assert.equal(foreign.data.error.code, host === node ? -32602 : -32002);
    assert.equal(foreign.data.result, undefined);
    const withoutReport = await host.result(
      "tools/call",
      { name: "csv_import_failure", arguments: args },
      "alice",
      ["imports:read"],
    );
    assert.ok(!withoutReport.content.some((c) => c.type === "resource_link"));
    const foreignRows = await host.result(
      "tools/call",
      {
        name: "csv_import_rows",
        arguments: { input: { import_id: importId, search: "" } },
      },
      "bob",
      support,
    );
    assert.equal(foreignRows.isError, true);
    const paid = await host.call("csv_import_payment", args, "alice");
    assert.equal(paid.structuredContent.items[0].amount_minor, 1000);
  }
  const first = await node.call(
    "csv_import_rows",
    { input: { import_id: importId, search: "" }, page_size: 1 },
    "alice",
  );
  const next = await pythonClient.call(
    "csv_import_rows",
    {
      input: { import_id: importId, search: "" },
      after: first.structuredContent.next_cursor,
    },
    "alice",
  );
  assert.equal(next.structuredContent.items.length, 1);
  assert.notEqual(
    first.structuredContent.items[0].input_id,
    next.structuredContent.items[0].input_id,
  );
  await pool.query(
    "UPDATE sdk_csv_query_imports SET enabled=false WHERE import_id=$1",
    [importId],
  );
  try {
    for (const host of [node, pythonClient])
      assert.equal(
        (await host.call("csv_import_failure", args, "alice")).isError,
        true,
      );
  } finally {
    await pool.query(
      "UPDATE sdk_csv_query_imports SET enabled=true WHERE import_id=$1",
      [importId],
    );
  }
  async function consumePythonTask(port, action, payload, binding) {
    const child = spawn(
      python,
      [new URL("pipeline_task_driver.py", import.meta.url).pathname],
      {
        env: {
          ...env,
          PYTHONPATH: [
            env.PYTHONPATH,
            process.env.GREGALE_MCP_STARTER_PYTHONPATH,
          ]
            .filter(Boolean)
            .join(delimiter),
        },
        stdio: ["pipe", "pipe", "pipe"],
        signal: AbortSignal.timeout(35000),
      },
    );
    let stdout = "",
      stderr = "";
    child.stdout.setEncoding("utf8").on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.setEncoding("utf8").on("data", (chunk) => {
      stderr += chunk;
    });
    child.stdin.end(
      JSON.stringify({
        action,
        ...payload,
        binding,
        endpoint: `http://127.0.0.1:${port}/mcp`,
        token: claims(privateKey, payload.subject ?? "alice", payload.scopes ?? operator, payload.clientId ?? ownerClient),
      }),
    );
    const [code] = await once(child, "close");
    assert.equal(code, 0, stderr);
    return JSON.parse(stdout);
  }
  return {
    node,
    python: pythonClient,
    pipelineTasks: Boolean(pipelineTasks),
    grantHandoff: (taskId, reviewerSubject) => pipelineTasks.grantHandoff(
      taskId, { reviewerSubject, ttlMs: 300_000 }, owningCaller, { timeoutMs: 10000 },
    ),
    revokeHandoff: (id) => pipelineTasks.revokeHandoff(id, owningCaller, { timeoutMs: 10000 }),
    async publishTaskRevision() {
      await pool.query(sdk.pipelineTaskRecoverySchema);
      const nextToolkit = await makeToolkit([retainedClarification, revisedClarification]);
      const next = revisedClarification.adapter.pipelineContract.tool;
      const upgradedNode = await launchNode(nextToolkit, sdk.createPipelineTasks(nextToolkit, {
        ...taskSettings,
        handoffs,
        tools: [...taskSettings.tools, next],
        inputForms: { [clarificationTool]: retainedClarification.input, [next]: revisedClarification.input },
        store: sdk.postgresPipelineTaskStore((q, v) => pool.query(q, v)),
        recovery: { scopes: ["imports:replace"], prepare: revisedClarification.prepareReplacement },
      }));
      const port = await launchPython({
        ...taskSettings,
        clarification: { ...taskSettings.clarification, publishV2: true },
      }, "toolkit-http-fixture-v2.json");
      return {
        host: client(port),
        node: client(upgradedNode),
        prepareBindings: () => prepareRevisedTaskBindings({ sdk, host: client(port), home, env, pythonCommand: python }),
        consume: (action, payload, binding) => consumePythonTask(port, action, payload, binding),
      };
    },
    prepareTaskBindings: (tool) =>
      preparePipelineTaskBindings({
        sdk,
        node,
        python: pythonClient,
        home,
        env,
        pythonCommand: python,
        tool,
      }),
  };
}
