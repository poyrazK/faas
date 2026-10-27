// H3 proxy (ADR-20261024082000): time the real challenge page's solver in
// headless Chrome, unthrottled and under CDP CPU throttling.
//
// The page is rebuilt from pkg/gateway/edge_rules_challenge_page.go (the
// same parts and solver script the gateway serves, with its CSP). Each
// navigation gets a fresh token of production length, so the work per trial
// is a fresh draw. The local server checks every submitted counter with
// SHA-256, as the gateway does. Per trial it records the wall time from
// serving the page to receiving the solution, the counter (the solver tries
// 0, 1, 2, ... so hashes = counter + 1) and the renderer's CPU time
// (SystemInfo.getProcessInfo), which other load on the machine does not
// inflate.
//
// usage: node h3.mjs <repo> <out.json> <rate:trials>...   e.g. 1:40 6:30 10:30
// CHROME=<binary> picks the browser (default: Google Chrome on macOS).
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { createHash, randomBytes, randomUUID } from "node:crypto";
import { tmpdir, loadavg } from "node:os";
import { join } from "node:path";

const [repo, outPath, ...plan] = process.argv.slice(2);
const DIFFICULTY = 16;
const SOLVE_PATH = "/.well-known/gregale-challenge";
const CHROME = process.env.CHROME || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

// --- the page, from the Go source ---------------------------------------
const src = readFileSync(join(repo, "pkg/gateway/edge_rules_challenge_page.go"), "utf8");
let js = src.match(/const challengeSolverJS = `([\s\S]*?)`\n/)[1];
// SOLVER=<file> swaps the hashing part for a candidate solver that defines
// solve(token, difficulty, start, limit); the page part stays the shipped one.
if (process.env.SOLVER) js = readFileSync(process.env.SOLVER, "utf8") + "\n" + js.slice(js.indexOf("if (typeof document"));
const block = src.match(/var challengePageParts = \[\.\.\.\]string\{([\s\S]*?)\n\}\n/)[1];
const raw = [...block.matchAll(/`([\s\S]*?)`/g)].map((m) => m[1]);
if (raw.length !== 10 || !block.includes("+ challengeSolverJS +")) throw new Error(`page parts changed: ${raw.length}`);
const parts = [...raw.slice(0, 8), raw[8] + js + raw[9]];
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&#34;").replace(/'/g, "&#39;");
function page(nonce, token) {
  const values = [nonce, "", SOLVE_PATH, token, String(DIFFICULTY), token, "/", nonce];
  return values.map((v, i) => parts[i] + esc(v)).join("") + parts[8];
}

// A token shaped like the gateway's: "c1." + base64url(JSON) + "." +
// base64url(16-byte MAC), JSON {a,r: UUIDs, n: 16-byte nonce, d, t, e}.
function token() {
  const body = JSON.stringify({
    a: randomUUID(), r: randomUUID(), n: randomBytes(16).toString("base64"),
    d: DIFFICULTY, t: 3600, e: Math.floor(Date.now() / 1000) + 300,
  });
  return "c1." + Buffer.from(body).toString("base64url") + "." + randomBytes(16).toString("base64url");
}
const workValid = (tok, counter) => /^[0-9]{1,20}$/.test(counter) &&
  Math.clz32(createHash("sha256").update(tok + ":" + counter).digest().readUInt32BE(0)) >= DIFFICULTY;

// --- the server -----------------------------------------------------------
let pending = null; // { token, servedAt, resolve }
const server = createServer((req, res) => {
  if (req.method === "GET" && req.url.startsWith("/?trial=")) {
    const nonce = randomBytes(16).toString("base64").replace(/=+$/, "");
    const tok = token();
    pending.token = tok;
    pending.servedAt = performance.now();
    res.writeHead(403, {
      "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store",
      "Content-Security-Policy": `default-src 'none'; script-src 'nonce-${nonce}'; style-src 'nonce-${nonce}'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`,
    });
    res.end(page(nonce, tok));
    return;
  }
  if (req.method === "POST" && req.url === SOLVE_PATH) {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      const at = performance.now();
      const f = new URLSearchParams(body);
      res.writeHead(200, { "Content-Type": "text/plain" });
      res.end("ok");
      if (pending && f.get("token") === pending.token) {
        pending.resolve({ counter: f.get("counter"), valid: workValid(pending.token, f.get("counter")), wallMs: at - pending.servedAt, tokenLen: pending.token.length });
      }
    });
    return;
  }
  res.writeHead(404); res.end();
});
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const origin = `http://127.0.0.1:${server.address().port}`;

// --- Chrome over CDP ------------------------------------------------------
const profile = mkdtempSync(join(process.env.TMPDIR || tmpdir(), "h3-chrome-"));
const chrome = spawn(CHROME, ["--headless=new", "--remote-debugging-port=0", `--user-data-dir=${profile}`,
  "--no-first-run", "--no-default-browser-check", "--disable-extensions",
  "--disable-background-timer-throttling", "--disable-renderer-backgrounding", "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
const wsURL = await new Promise((resolve, reject) => {
  let err = "";
  chrome.stderr.on("data", (d) => { err += d; const m = err.match(/DevTools listening on (ws:\/\/\S+)/); if (m) resolve(m[1]); });
  setTimeout(() => reject(new Error("chrome did not start: " + err.slice(-400))), 30000);
});
const ws = new WebSocket(wsURL);
await new Promise((r) => ws.addEventListener("open", r));
let seq = 0;
const waiting = new Map();
ws.addEventListener("message", (e) => {
  const m = JSON.parse(e.data);
  if (m.id && waiting.has(m.id)) { const w = waiting.get(m.id); waiting.delete(m.id); m.error ? w.reject(new Error(JSON.stringify(m.error))) : w.resolve(m.result); }
});
const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
  const id = ++seq; waiting.set(id, { resolve, reject });
  ws.send(JSON.stringify(sessionId ? { id, method, params, sessionId } : { id, method, params }));
});
const { targetId } = await send("Target.createTarget", { url: "about:blank" });
const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
await send("Page.enable", {}, sessionId);
const rendererCPU = async () => (await send("SystemInfo.getProcessInfo")).processInfo
  .filter((p) => p.type === "renderer").reduce((s, p) => s + p.cpuTime, 0);

// --- trials ---------------------------------------------------------------
const results = [];
for (const step of plan) {
  const [rate, trials] = step.split(":").map(Number);
  for (let i = 0; i < trials; i++) {
    await send("Emulation.setCPUThrottlingRate", { rate }, sessionId);
    const cpu0 = await rendererCPU();
    const got = new Promise((resolve) => { pending = { resolve }; });
    const timeout = new Promise((_, reject) => setTimeout(() => reject(new Error(`trial timed out at rate ${rate}`)), 180000));
    await send("Page.navigate", { url: `${origin}/?trial=${rate}-${i}` }, sessionId);
    const r = await Promise.race([got, timeout]);
    const cpu1 = await rendererCPU();
    const row = { rate, trial: i, hashes: Number(r.counter) + 1, valid: r.valid, wallMs: +r.wallMs.toFixed(1), cpuMs: +((cpu1 - cpu0) * 1000).toFixed(1), tokenLen: r.tokenLen, load1: +loadavg()[0].toFixed(1) };
    results.push(row);
    console.log(JSON.stringify(row));
  }
}
writeFileSync(outPath, JSON.stringify(results, null, 1));
ws.close(); server.close();
await new Promise((r) => { chrome.once("exit", r); chrome.kill(); });
rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 500 });
