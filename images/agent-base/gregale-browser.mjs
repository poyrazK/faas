#!/usr/bin/env node
// gregale-browser — the sandbox browser (ADR-20261112121456 phase 2,
// ADR-20261027090014 §4.3). It starts headless Chromium with its DevTools
// protocol (CDP) on 127.0.0.1:9221 and serves CDP on 127.0.0.1:9222 through a
// relay, the port a session exposes at its preview URL.
//
// The relay exists because a preview request keeps its preview host as Host,
// and Chromium refuses DevTools requests whose Host is not localhost or an
// address. The relay sends Chromium Host: 127.0.0.1:9221 and rewrites the
// WebSocket URLs Chromium answers with to the caller's host, so
// `chromium.connectOverCDP(previewURL, {headers})` works from outside.
//
// Usage:
//   gregale-browser [-- chromium flags...]   start; runs until Chromium exits
//   gregale-browser --wait [seconds]         exit 0 once CDP answers
//   gregale-browser --prewarm                start Chromium, wait for it,
//                                            stop it and delete its profile
//                                            (a warm snapshot's capture,
//                                            ADR-20261116182615 §2)
//   gregale-browser fetch [--max-chars N] [--timeout S] -- <url>
//                                            load url in a new tab and print
//                                            {url, final_url, status, title,
//                                            text, truncated} (a browse
//                                            task, ADR-20261116182615 §3)
//
// Only Node built-ins: it runs from the read-only agent-base drive0.
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, rmSync } from 'node:fs';
import http from 'node:http';
import net from 'node:net';

const RELAY_PORT = Number(process.env.GREGALE_BROWSER_PORT || 9222);
const CDP_PORT = RELAY_PORT - 1;
const CDP_HOST = `127.0.0.1:${CDP_PORT}`;

// isLocal reports whether a Host header names this machine, so answers keep
// plain ws:// for callers inside the sandbox.
export function isLocal(host) {
  const name = (host || '').replace(/:\d+$/, '').replace(/^\[|\]$/g, '');
  return name === 'localhost' || name === '127.0.0.1' || name === '::1';
}

// rewriteBody points the WebSocket URLs in a /json answer at the caller's
// host: wss:// through a preview URL, ws:// inside the sandbox.
export function rewriteBody(body, host) {
  if (!host) {
    return body;
  }
  const scheme = isLocal(host) ? 'ws' : 'wss';
  return body.split(`ws://${CDP_HOST}`).join(`${scheme}://${host}`).split(`ws=${CDP_HOST}`).join(`${scheme}=${host}`);
}

// upgradeHead is the WebSocket upgrade request sent to Chromium: the
// caller's request with Host set to Chromium's and Origin dropped.
export function upgradeHead(req) {
  const lines = [`${req.method} ${req.url} HTTP/1.1`, `Host: ${CDP_HOST}`];
  for (let i = 0; i < req.rawHeaders.length; i += 2) {
    const name = req.rawHeaders[i].toLowerCase();
    if (name !== 'host' && name !== 'origin') {
      lines.push(`${req.rawHeaders[i]}: ${req.rawHeaders[i + 1]}`);
    }
  }
  return lines.join('\r\n') + '\r\n\r\n';
}

// relay serves CDP on RELAY_PORT, forwarding to Chromium on CDP_PORT.
export function relay() {
  const server = http.createServer((req, res) => {
    const headers = { ...req.headers, host: CDP_HOST };
    delete headers.origin;
    const up = http.request({ host: '127.0.0.1', port: CDP_PORT, method: req.method, path: req.url, headers }, (cdp) => {
      if (!req.url.startsWith('/json')) {
        res.writeHead(cdp.statusCode, cdp.headers);
        cdp.pipe(res);
        return;
      }
      const chunks = [];
      cdp.on('data', (c) => chunks.push(c));
      cdp.on('end', () => {
        const body = Buffer.from(rewriteBody(Buffer.concat(chunks).toString('utf8'), req.headers.host), 'utf8');
        const out = { ...cdp.headers, 'content-length': String(body.length) };
        delete out['transfer-encoding'];
        res.writeHead(cdp.statusCode, out);
        res.end(body);
      });
    });
    up.on('error', () => {
      res.writeHead(502, { 'content-type': 'text/plain' });
      res.end('gregale-browser: Chromium is not answering\n');
    });
    req.pipe(up);
  });
  server.on('upgrade', (req, socket, head) => {
    const cdp = net.connect(CDP_PORT, '127.0.0.1', () => {
      cdp.write(upgradeHead(req));
      if (head.length > 0) {
        cdp.write(head);
      }
      cdp.pipe(socket);
      socket.pipe(cdp);
    });
    const close = () => {
      cdp.destroy();
      socket.destroy();
    };
    cdp.on('error', close);
    socket.on('error', close);
  });
  server.listen(RELAY_PORT, '127.0.0.1');
  return server;
}

function chromiumPath() {
  for (const p of [process.env.GREGALE_BROWSER_CHROMIUM, '/usr/bin/chromium', '/usr/bin/chromium-browser']) {
    if (existsSync(p)) {
      return p;
    }
  }
  throw new Error('gregale-browser: no Chromium in this image');
}

function versionOK(port) {
  return new Promise((resolve) => {
    const req = http.get({ host: '127.0.0.1', port, path: '/json/version', timeout: 1000 }, (res) => {
      res.resume();
      resolve(res.statusCode === 200);
    });
    req.on('error', () => resolve(false));
    req.on('timeout', () => {
      req.destroy();
      resolve(false);
    });
  });
}

async function waitFor(port, seconds) {
  const deadline = Date.now() + seconds * 1000;
  while (Date.now() < deadline) {
    if (await versionOK(port)) {
      return true;
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  return false;
}

function profileDir() {
  return process.env.GREGALE_BROWSER_PROFILE || '/tmp/gregale-browser';
}

// launch starts Chromium with CDP on CDP_PORT.
function launch(profile, extra) {
  mkdirSync(profile, { recursive: true });
  // --no-sandbox: the microVM is the sandbox, and the guest has no user
  // namespaces for Chromium's own. The background flags keep Chromium from
  // calling home (updates, sync, metrics) through the session's egress.
  const args = ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-first-run',
    '--no-default-browser-check', '--disable-background-networking', '--disable-component-update', '--disable-sync',
    '--disable-domain-reliability', '--metrics-recording-only', '--no-pings', '--remote-debugging-address=127.0.0.1', `--remote-debugging-port=${CDP_PORT}`,
    '--remote-allow-origins=*', `--user-data-dir=${profile}`, ...extra, 'about:blank'];
  return spawn(chromiumPath(), args, { stdio: ['ignore', 'inherit', 'inherit'] });
}

// prewarm runs Chromium once and stops it, leaving its files in the page
// cache and nothing running or written: a snapshot captured afterwards has
// no Chromium state two restores could share (CLAUDE.md invariant 5).
async function prewarm() {
  const profile = profileDir();
  const chromium = launch(profile, []);
  const exited = new Promise((r) => chromium.on('exit', r));
  const ok = await waitFor(CDP_PORT, 60);
  chromium.kill('SIGTERM');
  const killer = setTimeout(() => chromium.kill('SIGKILL'), 10_000);
  await exited;
  clearTimeout(killer);
  rmSync(profile, { recursive: true, force: true });
  if (!ok) {
    console.error('gregale-browser: Chromium did not answer on its DevTools port within 60 s');
    process.exit(1);
  }
  console.log('gregale-browser: prewarmed');
}

async function start(extra) {
  const chromium = launch(profileDir(), extra);
  chromium.on('exit', (code, signal) => process.exit(code ?? (signal ? 1 : 0)));
  for (const sig of ['SIGINT', 'SIGTERM']) {
    process.on(sig, () => chromium.kill(sig));
  }
  if (!(await waitFor(CDP_PORT, 30))) {
    chromium.kill('SIGKILL');
    console.error('gregale-browser: Chromium did not answer on its DevTools port within 30 s');
    process.exit(1);
  }
  relay();
  console.log(`gregale-browser: CDP at http://127.0.0.1:${RELAY_PORT}`);
}

// cdpSession speaks CDP over one WebSocket: send resolves with a command's
// result, and on(method) hears its events.
export function cdpSession(ws) {
  let next = 0;
  const pending = new Map();
  const listeners = new Map();
  ws.addEventListener('message', (ev) => {
    const msg = JSON.parse(typeof ev.data === 'string' ? ev.data : Buffer.from(ev.data).toString('utf8'));
    if (msg.id !== undefined && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) {
        reject(new Error(msg.error.message || 'CDP error'));
      } else {
        resolve(msg.result);
      }
      return;
    }
    for (const fn of listeners.get(msg.method) || []) {
      fn(msg.params || {});
    }
  });
  ws.addEventListener('close', () => {
    for (const { reject } of pending.values()) {
      reject(new Error('the browser closed the connection'));
    }
    pending.clear();
  });
  return {
    send(method, params = {}) {
      const id = ++next;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        ws.send(JSON.stringify({ id, method, params }));
      });
    },
    on(method, fn) {
      listeners.set(method, [...(listeners.get(method) || []), fn]);
    },
  };
}

// fetchArgs reads fetch's flags; the URL comes after --, so it is never a
// flag.
export function fetchArgs(argv) {
  const dash = argv.indexOf('--');
  const flags = dash >= 0 ? argv.slice(0, dash) : argv;
  const url = dash >= 0 ? argv[dash + 1] : undefined;
  const out = { maxChars: 20000, timeout: 60, url };
  for (let i = 0; i < flags.length; i += 2) {
    const value = Number(flags[i + 1]);
    if (!Number.isInteger(value) || value < 1) {
      throw new Error(`gregale-browser fetch: ${flags[i]} needs a positive integer`);
    }
    if (flags[i] === '--max-chars') {
      out.maxChars = value;
    } else if (flags[i] === '--timeout') {
      out.timeout = value;
    } else {
      throw new Error(`gregale-browser fetch: unknown flag ${flags[i]}`);
    }
  }
  if (!url || !/^https?:\/\//i.test(url)) {
    throw new Error('gregale-browser fetch: give an http or https URL after --');
  }
  return out;
}

// pageResult is fetch's output: the page's text cut to maxChars.
export function pageResult(url, finalURL, status, title, text, maxChars) {
  const chars = Array.from(text || '');
  const truncated = chars.length > maxChars;
  return {
    url,
    final_url: finalURL || url,
    status: status || 0,
    title: Array.from(title || '').slice(0, 1024).join(''),
    text: truncated ? chars.slice(0, maxChars).join('') : text || '',
    truncated,
  };
}

const PAGE_TEXT = `({title: document.title, url: location.href,
  text: document.body ? document.body.innerText : (document.documentElement ? document.documentElement.textContent : '')})`;

// fetchPage loads url in a new tab of the Chromium on port and reads its
// text once the page loaded, or what it has when the time is nearly up.
export async function fetchPage(port, { url, maxChars, timeout }) {
  const deadline = Date.now() + timeout * 1000;
  const target = await new Promise((resolve, reject) => {
    const req = http.request({ host: '127.0.0.1', port, method: 'PUT', path: '/json/new?about:blank' }, (res) => {
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => {
        try {
          resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')));
        } catch {
          reject(new Error('the browser did not open a tab'));
        }
      });
    });
    req.on('error', reject);
    req.end();
  });
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', () => reject(new Error('the browser refused the tab connection')), { once: true });
  });
  const cdp = cdpSession(ws);
  try {
    let frameId;
    let status = 0;
    let loaded;
    const load = new Promise((r) => (loaded = r));
    cdp.on('Page.loadEventFired', () => loaded());
    cdp.on('Network.responseReceived', (p) => {
      if (p.type === 'Document' && (!frameId || p.frameId === frameId)) {
        status = p.response.status;
      }
    });
    await cdp.send('Page.enable');
    await cdp.send('Network.enable');
    const nav = await cdp.send('Page.navigate', { url });
    if (nav.errorText) {
      throw new Error(`could not load ${url}: ${nav.errorText}`);
    }
    frameId = nav.frameId;
    // Leave a fifth of the time, at least 2 s, to read the page.
    const wait = Math.max(0, deadline - Date.now() - Math.max(2000, timeout * 200));
    await Promise.race([load, new Promise((r) => setTimeout(r, wait))]);
    const { result } = await cdp.send('Runtime.evaluate', { expression: PAGE_TEXT, returnByValue: true });
    const page = result.value || {};
    return pageResult(url, page.url, status, page.title, page.text, maxChars);
  } finally {
    ws.close();
    http.get({ host: '127.0.0.1', port, path: `/json/close/${target.id}` }).on('error', () => {});
  }
}

// fetchMain runs a browse task: it uses the session's Chromium, starting one
// when none is running, and prints the page as JSON. A failure prints its
// reason on stdout, which is what the task returns, and exits 1.
async function fetchMain(argv) {
  let chromium;
  let timer;
  let code = 0;
  try {
    const args = fetchArgs(argv);
    if (!(await waitFor(CDP_PORT, 2))) {
      chromium = launch(profileDir(), []);
      if (!(await waitFor(CDP_PORT, 30))) {
        throw new Error('Chromium did not answer on its DevTools port within 30 s');
      }
    }
    const late = new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`gregale-browser fetch: ${args.url} did not load within ${args.timeout} s`)),
        args.timeout * 1000);
    });
    const page = await Promise.race([fetchPage(CDP_PORT, args), late]);
    process.stdout.write(JSON.stringify(page) + '\n');
  } catch (err) {
    console.log(err.message);
    code = 1;
  } finally {
    clearTimeout(timer);
  }
  if (chromium) {
    const exited = new Promise((r) => chromium.on('exit', r));
    chromium.kill('SIGTERM');
    await Promise.race([exited, new Promise((r) => setTimeout(r, 5000))]);
    chromium.kill('SIGKILL');
    rmSync(profileDir(), { recursive: true, force: true });
  }
  process.exit(code);
}

async function main(argv) {
  if (argv[0] === 'fetch') {
    await fetchMain(argv.slice(1));
    return;
  }
  if (argv[0] === '--wait') {
    const seconds = Number(argv[1] || 30);
    process.exit((await waitFor(RELAY_PORT, seconds)) ? 0 : 1);
  }
  if (argv[0] === '--prewarm') {
    await prewarm();
    return;
  }
  const dash = argv.indexOf('--');
  await start(dash >= 0 ? argv.slice(dash + 1) : []);
}

if (import.meta.url === `file://${process.argv[1]}` || process.argv[1]?.endsWith('/gregale-browser')) {
  main(process.argv.slice(2)).catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}
