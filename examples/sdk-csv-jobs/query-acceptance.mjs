import assert from "node:assert/strict";
import { createServer } from "node:http";
import { createHash, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import { csvQueries } from "./query.mjs";

/** Called after compensation planning verified the original failed native pipeline, before approval. */
export async function queryAcceptance({
  sdk,
  pool,
  scope,
  importId,
  sourceRunId,
  env,
  pythonCall,
  page,
  cleanup,
}) {
  await pool.query(
    await readFile(new URL("query-schema.sql", import.meta.url), "utf8"),
  );
  const summary = `import_id,source_run_id,stage,status\n${importId},${sourceRunId},report,failed\n`;
  const digest = (text) => createHash("sha256").update(text).digest("hex");
  await pool.query(
    "INSERT INTO sdk_csv_query_imports(import_id,owner_id,source_run_id,failure_report,report_sha256) VALUES($1,$2,$3,$4,$5)",
    [importId, "alice", sourceRunId, summary, digest(summary)],
  );
  const { tools, report, adapters } = await csvQueries({
      sdk,
      pool,
      scope,
      cursorKey: env.GREGALE_QUERY_CURSOR_KEY,
    }),
    options = { timeoutMs: 10000 },
    input = { import_id: importId, search: "" };
  const pyCall = (kind, args, access = "alice") =>
    pythonCall({
      action: "query-call",
      kind,
      name: tools[kind].name,
      arguments: args,
      access,
    });
  const results = await Promise.all([
    adapters.rows.call(
      tools.rows.name,
      { input, page_size: 1 },
      "alice",
      options,
    ),
    pyCall("rows", { input, page_size: 1 }),
  ]);
  assert.deepEqual(results[0], results[1]);
  const first = results[0].structuredContent;
  assert.equal(first.items.length, 1);
  assert.ok(first.next_cursor);
  const second = await pyCall("rows", {
    input,
    page_size: 1,
    after: first.next_cursor,
  });
  assert.equal(second.structuredContent.items.length, 1);
  assert.equal(second.structuredContent.next_cursor, null);
  assert.notEqual(
    first.items[0].input_id,
    second.structuredContent.items[0].input_id,
  );
  assert.equal(
    (await pyCall("rows", { input, after: first.next_cursor }, "bob")).isError,
    true,
  );

  // An independently authorized second import is visible only to its own principal.
  const bobImport = randomUUID();
  await pool.query(
    "INSERT INTO sdk_csv_query_imports(import_id,owner_id,source_run_id,failure_report,report_sha256) VALUES($1,$2,$3,$4,$5)",
    [bobImport, "bob", sourceRunId, "bob report", digest("bob report")],
  );
  await pool.query(
    "INSERT INTO sdk_csv_rows(input_id,row_number,email) VALUES($1,1,$2)",
    [bobImport + ":1", "private-bob@example.test"],
  );
  const isolation = await Promise.all([
    tools.rows.page(input, "alice", options),
    pyCall("rows", { input: { import_id: bobImport, search: "" } }, "bob"),
  ]);
  assert.equal(isolation[0].items.length, 2);
  assert.equal(
    isolation[1].structuredContent.items[0].email,
    "private-bob@example.test",
  );
  assert.equal(
    (
      await adapters.rows.call(
        tools.rows.name,
        { input: { import_id: bobImport, search: "" } },
        "alice",
        options,
      )
    ).isError,
    true,
  );
  await pool.query("DELETE FROM sdk_csv_rows WHERE input_id=$1", [
    bobImport + ":1",
  ]);
  await pool.query("DELETE FROM sdk_csv_query_imports WHERE import_id=$1", [
    bobImport,
  ]);

  const failure = await adapters.failure.call(
    tools.failure.name,
    { input: { import_id: importId } },
    "alice",
    options,
  );
  assert.deepEqual(
    failure,
    await pyCall("failure", { input: { import_id: importId } }),
  );
  assert.equal(failure.structuredContent.items[0].status, "failed");
  const link = failure.content.find((c) => c.type === "resource_link");
  assert.ok(link);
  const contents = await adapters.failure.readResource(
    link.uri,
    "alice",
    options,
  );
  assert.equal(contents.contents[0].text, summary);
  assert.deepEqual(
    contents,
    await pythonCall({ action: "query-read", uri: link.uri, access: "alice" }),
  );
  assert.deepEqual(
    await pythonCall({ action: "query-read", uri: link.uri, access: "bob" }),
    { code: -32002 },
  );
  const payment = await pyCall("payment", { input: { import_id: importId } });
  assert.deepEqual(
    payment,
    await adapters.payment.call(
      tools.payment.name,
      { input: { import_id: importId } },
      "alice",
      options,
    ),
  );
  assert.equal(payment.structuredContent.items[0].amount_minor, 1000);
  assert.equal(payment.structuredContent.items[0].currency, "usd");

  await pool.query(
    "UPDATE sdk_csv_query_imports SET enabled=false WHERE import_id=$1",
    [importId],
  );
  assert.equal(
    (await pyCall("rows", { input, after: first.next_cursor })).isError,
    true,
  );
  await assert.rejects(report.read(link.uri, "alice", options), {
    code: "unauthorized",
  });
  await pool.query(
    "UPDATE sdk_csv_query_imports SET enabled=true WHERE import_id=$1",
    [importId],
  );
  await pool.query(
    "UPDATE sdk_csv_query_imports SET failure_report='tampered' WHERE import_id=$1",
    [importId],
  );
  await assert.rejects(report.read(link.uri, "alice", options), {
    code: "invalid_output",
  });
  await pool.query(
    "UPDATE sdk_csv_query_imports SET failure_report=$2 WHERE import_id=$1",
    [importId, summary],
  );

  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://localhost");
    const send = (code, body) => {
      res.writeHead(code, {
        "content-type": "application/json",
        "cache-control": "no-store",
      });
      res.end(JSON.stringify(body));
    };
    if (url.pathname === "/") {
      res.writeHead(200, {
        "content-type": "text/html",
        "cache-control": "no-store",
      });
      res.end(`<!doctype html><title>Import recovery context</title><h1>Inspect import before recovery</h1><label>Session token <input id="token" type="password"></label><label>Search email <input id="search"></label><button id="rows">Search rows</button><button id="failure">Read failure report</button><button id="payment">Read payment</button><pre id="context"></pre><p id="status"></p><script>
const data=document.querySelector('#context'),status=document.querySelector('#status'),token=document.querySelector('#token');token.oninput=()=>{data.textContent='';status.textContent='';};const headers=()=>({'authorization':'Bearer '+token.value,'content-type':'application/json'});for(const kind of ['rows','failure','payment'])document.querySelector('#'+kind).onclick=async()=>{data.textContent='';const input={import_id:${JSON.stringify(importId)}};if(kind==='rows')input.search=document.querySelector('#search').value;const response=await fetch('/query/'+kind,{method:'POST',headers:headers(),body:JSON.stringify({input})});const result=await response.json();if(!response.ok||result.isError){status.textContent='Denied';return;}if(kind==='failure'){const link=result.content.find(c=>c.type==='resource_link');const report=await fetch('/resource?uri='+encodeURIComponent(link.uri),{headers:headers()});const body=await report.json();if(!report.ok){status.textContent='Denied';return;}data.textContent=body.contents[0].text;}else data.textContent=JSON.stringify(result.structuredContent.items);status.textContent='Loaded';};</script>`);
      return;
    }
    const access =
      req.headers.authorization === "Bearer alice-review"
        ? "alice"
        : req.headers.authorization === "Bearer bob-review"
          ? "bob"
          : null;
    if (!access) {
      send(401, {});
      return;
    }
    try {
      if (req.method === "GET" && url.pathname === "/resource") {
        send(
          200,
          await adapters.failure.readResource(
            url.searchParams.get("uri"),
            access,
            options,
          ),
        );
        return;
      }
      const kind = url.pathname.slice("/query/".length);
      if (
        req.method !== "POST" ||
        !url.pathname.startsWith("/query/") ||
        !Object.hasOwn(adapters, kind)
      ) {
        send(404, {});
        return;
      }
      const chunks = [];
      let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 16384) {
          send(413, {});
          return;
        }
        chunks.push(chunk);
      }
      send(
        200,
        await adapters[kind].call(
          tools[kind].name,
          JSON.parse(Buffer.concat(chunks).toString()),
          access,
          options,
        ),
      );
    } catch (e) {
      send(e.code === -32002 ? 404 : 400, {
        code: typeof e.code === "number" ? e.code : "query_failed",
      });
    }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  cleanup.push(() => new Promise((resolve) => server.close(resolve)));
  await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.fill("#token", "alice-review");
  await page.click("#rows");
  await page.waitForFunction(
    () => document.querySelector("#status").textContent === "Loaded",
  );
  assert.ok(
    (await page.textContent("#context")).includes(first.items[0].email),
  );
  await page.click("#failure");
  await page.waitForFunction(() =>
    document.querySelector("#context").textContent.includes(",report,failed"),
  );
  await page.click("#payment");
  await page.waitForFunction(() =>
    document.querySelector("#context").textContent.includes("pi_csv"),
  );
  await page.fill("#token", "bob-review");
  assert.equal(await page.textContent("#context"), "");
  await page.click("#failure");
  await page.waitForFunction(
    () => document.querySelector("#status").textContent === "Denied",
  );
  assert.equal(await page.textContent("#context"), "");
  return { tools, report, adapters };
}
