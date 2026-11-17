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
//   gregale-browser act <kind> [--ref N] [--key K] [--direction up|down]
//                       [--submit] [--max-chars N] [--timeout S]
//                       [--screenshot PATH] [-- value]
//                                            act on the session's page and
//                                            print its snapshot {url, title,
//                                            status, elements, text,
//                                            truncated} (a browser action,
//                                            ADR-20261117065818); kind is
//                                            navigate (value: url), click,
//                                            type (value: text), press,
//                                            scroll, back or snapshot;
//                                            --screenshot also writes the
//                                            viewport as a JPEG to PATH
//                                            (ADR-20261117065818 §5)
//
// Only Node built-ins: it runs from the read-only agent-base drive0.
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
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
    '--no-default-browser-check', '--window-size=1280,800', '--disable-background-networking', '--disable-component-update', '--disable-sync',
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

// ACT_KINDS are the actions act takes (ADR-20261117065818 §1); ACT_KEYS the
// keys press may press, with their CDP key event fields.
export const ACT_KINDS = ['navigate', 'click', 'type', 'press', 'scroll', 'back', 'snapshot'];
export const ACT_KEYS = {
  Enter: { key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, text: '\r' },
  Tab: { key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 },
  Escape: { key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 },
  Backspace: { key: 'Backspace', code: 'Backspace', windowsVirtualKeyCode: 8 },
  ArrowUp: { key: 'ArrowUp', code: 'ArrowUp', windowsVirtualKeyCode: 38 },
  ArrowDown: { key: 'ArrowDown', code: 'ArrowDown', windowsVirtualKeyCode: 40 },
  ArrowLeft: { key: 'ArrowLeft', code: 'ArrowLeft', windowsVirtualKeyCode: 37 },
  ArrowRight: { key: 'ArrowRight', code: 'ArrowRight', windowsVirtualKeyCode: 39 },
  PageUp: { key: 'PageUp', code: 'PageUp', windowsVirtualKeyCode: 33 },
  PageDown: { key: 'PageDown', code: 'PageDown', windowsVirtualKeyCode: 34 },
  Home: { key: 'Home', code: 'Home', windowsVirtualKeyCode: 36 },
  End: { key: 'End', code: 'End', windowsVirtualKeyCode: 35 },
};
const ACT_ELEMENTS_MAX = 150;

// actArgs reads act's kind and flags. A value (navigate's URL, type's
// text) comes after --, so it is never a flag.
export function actArgs(argv) {
  const kind = argv[0];
  if (!ACT_KINDS.includes(kind)) {
    throw new Error(`gregale-browser act: kind must be one of ${ACT_KINDS.join(', ')}`);
  }
  const rest = argv.slice(1);
  const dash = rest.indexOf('--');
  const flags = dash >= 0 ? rest.slice(0, dash) : rest;
  const out = { kind, maxChars: 4000, timeout: 30, value: dash >= 0 ? rest[dash + 1] : undefined, submit: false };
  for (let i = 0; i < flags.length; i++) {
    const flag = flags[i];
    if (flag === '--submit') {
      out.submit = true;
      continue;
    }
    const raw = flags[++i];
    if (flag === '--key') {
      if (!Object.hasOwn(ACT_KEYS, raw)) {
        throw new Error(`gregale-browser act: --key must be one of ${Object.keys(ACT_KEYS).join(', ')}`);
      }
      out.key = raw;
      continue;
    }
    if (flag === '--screenshot') {
      if (!/^\/workspace\/\.gregale-screenshot-[0-9a-f-]{36}\.jpg$/.test(raw || '')) {
        throw new Error('gregale-browser act: --screenshot must be /workspace/.gregale-screenshot-<uuid>.jpg');
      }
      out.screenshot = raw;
      continue;
    }
    if (flag === '--direction') {
      if (raw !== 'up' && raw !== 'down') {
        throw new Error('gregale-browser act: --direction must be up or down');
      }
      out.direction = raw;
      continue;
    }
    const value = Number(raw);
    if (!Number.isInteger(value) || value < 1) {
      throw new Error(`gregale-browser act: ${flag} needs a positive integer`);
    }
    if (flag === '--ref') {
      out.ref = value;
    } else if (flag === '--max-chars') {
      out.maxChars = value;
    } else if (flag === '--timeout') {
      out.timeout = value;
    } else {
      throw new Error(`gregale-browser act: unknown flag ${flag}`);
    }
  }
  const need = {
    navigate: () => out.value && /^https?:\/\//i.test(out.value),
    click: () => out.ref,
    type: () => out.ref && out.value !== undefined,
    press: () => out.key,
    scroll: () => out.direction,
    back: () => true,
    snapshot: () => true,
  };
  if (!need[kind]()) {
    throw new Error(`gregale-browser act ${kind}: missing its argument (navigate: -- <url>; click: --ref; type: --ref and -- <text>; press: --key; scroll: --direction)`);
  }
  return out;
}

// SNAPSHOT is the page script that stamps refs on the visible interactive
// elements and reads them with the page's text. Its arguments are numbers
// act validated; nothing a caller typed is evaluated.
export function snapshotScript(maxChars, limit = ACT_ELEMENTS_MAX) {
  return `(() => {
  const maxChars = ${Number(maxChars)}, limit = ${Number(limit)};
  for (const el of document.querySelectorAll('[data-gregale-ref]')) el.removeAttribute('data-gregale-ref');
  const sel = 'a[href],button,input:not([type=hidden]),select,textarea,summary,[contenteditable=""],[contenteditable=true],' +
    '[role=button],[role=link],[role=checkbox],[role=radio],[role=tab],[role=menuitem],[role=option],[role=switch],' +
    '[role=textbox],[role=combobox],[role=searchbox]';
  const clean = (v) => String(v || '').replace(/\\s+/g, ' ').trim().slice(0, 100);
  const roleOf = (el) => {
    const tag = el.tagName.toLowerCase();
    if (tag === 'a') return 'link';
    if (tag === 'select') return 'combobox';
    if (tag === 'textarea') return 'textbox';
    if (tag === 'input') {
      const t = (el.getAttribute('type') || 'text').toLowerCase();
      if (t === 'checkbox' || t === 'radio') return t;
      if (t === 'submit' || t === 'button' || t === 'reset' || t === 'image') return 'button';
      return t === 'search' ? 'searchbox' : 'textbox';
    }
    if (el.isContentEditable) return 'textbox';
    return tag === 'summary' ? 'button' : tag;
  };
  const elements = [];
  let more = false;
  for (const el of document.querySelectorAll(sel)) {
    const box = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    if (box.width === 0 || box.height === 0 || style.visibility === 'hidden' || style.display === 'none' || el.disabled) continue;
    if (elements.length >= limit) { more = true; break; }
    const ref = elements.length + 1;
    el.setAttribute('data-gregale-ref', String(ref));
    const role = el.getAttribute('role') || roleOf(el);
    const label = el.labels && el.labels[0] ? el.labels[0].innerText : '';
    const name = clean(el.getAttribute('aria-label') || label || el.getAttribute('placeholder') || el.innerText ||
      (el.type === 'submit' || el.type === 'button' ? el.value : '') || el.getAttribute('title') || el.getAttribute('alt'));
    const e = { ref, role, name };
    if ((role === 'textbox' || role === 'searchbox' || role === 'combobox') && el.type !== 'password' && 'value' in el) {
      e.value = clean(el.value);
    }
    if (role === 'checkbox' || role === 'radio') e.checked = !!el.checked;
    elements.push(e);
  }
  const all = Array.from(document.body ? document.body.innerText : '');
  return { url: location.href, title: document.title.slice(0, 1024), elements, more_elements: more,
    text: all.slice(0, maxChars).join(''), truncated: all.length > maxChars };
})()`;
}

// refScript finds a ref's element, scrolls it into view and returns its
// centre, or null when the ref is not on the page.
export function refScript(ref) {
  return `(() => {
  const el = document.querySelector('[data-gregale-ref="${Number(ref)}"]');
  if (!el) return null;
  el.scrollIntoView({ block: 'center', inline: 'center' });
  const box = el.getBoundingClientRect();
  return { x: box.left + box.width / 2, y: box.top + box.height / 2 };
})()`;
}

// clearScript focuses a ref's element and clears what it holds, so type
// replaces it.
function clearScript(ref) {
  return `(() => {
  const el = document.querySelector('[data-gregale-ref="${Number(ref)}"]');
  if (!el) return false;
  el.focus();
  if ('value' in el && typeof el.select === 'function') { el.select(); }
  else if (el.isContentEditable) { document.getSelection().selectAllChildren(el); }
  return true;
})()`;
}

async function cdpJSON(port, method, path) {
  return new Promise((resolve, reject) => {
    const req = http.request({ host: '127.0.0.1', port, method, path }, (res) => {
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => {
        try {
          resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')));
        } catch {
          reject(new Error(`the browser answered ${path} with no JSON`));
        }
      });
    });
    req.on('error', reject);
    req.end();
  });
}

// sessionPage is the session's one page: the first page target, or a new
// one when there is none.
async function sessionPage(port) {
  const targets = await cdpJSON(port, 'GET', '/json/list');
  const page = targets.find((t) => t.type === 'page');
  return page || cdpJSON(port, 'PUT', '/json/new?about:blank');
}

// actOn takes one action on the session's page and returns its snapshot.
// Refs belong to the last snapshot, so an unknown ref fails rather than
// acting elsewhere.
export async function actOn(port, args) {
  const deadline = Date.now() + args.timeout * 1000;
  const target = await sessionPage(port);
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', () => reject(new Error('the browser refused the page connection')), { once: true });
  });
  const cdp = cdpSession(ws);
  try {
    let status = 0;
    let loading = false;
    let loaded;
    let load = new Promise((r) => (loaded = r));
    cdp.on('Page.frameStartedLoading', () => {
      loading = true;
    });
    cdp.on('Page.loadEventFired', () => {
      loading = false;
      loaded();
      load = new Promise((r) => (loaded = r));
    });
    cdp.on('Network.responseReceived', (p) => {
      if (p.type === 'Document') {
        status = p.response.status;
      }
    });
    await cdp.send('Page.enable');
    await cdp.send('Network.enable');
    await cdp.send('Page.setDownloadBehavior', { behavior: 'deny' }).catch(() => {});
    const left = () => Math.max(0, deadline - Date.now() - 2000);
    // settle waits for a load the action started, or briefly for one to
    // start, leaving time to read the page.
    const settle = async (waitStart) => {
      await new Promise((r) => setTimeout(r, Math.min(waitStart, left())));
      if (loading) {
        await Promise.race([load, new Promise((r) => setTimeout(r, left()))]);
      }
    };
    const evaluate = async (expression) => {
      const { result, exceptionDetails } = await cdp.send('Runtime.evaluate', { expression, returnByValue: true });
      if (exceptionDetails) {
        throw new Error('the page refused the snapshot script');
      }
      return result.value;
    };
    const point = async () => {
      const at = await evaluate(refScript(args.ref));
      if (!at) {
        throw new Error(`ref ${args.ref} is not on the page; take a snapshot and use one of its refs`);
      }
      return at;
    };
    const key = async (name) => {
      const k = ACT_KEYS[name];
      await cdp.send('Input.dispatchKeyEvent', { type: k.text ? 'keyDown' : 'rawKeyDown', ...k });
      await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: k.key, code: k.code, windowsVirtualKeyCode: k.windowsVirtualKeyCode });
    };
    switch (args.kind) {
      case 'navigate': {
        const nav = await cdp.send('Page.navigate', { url: args.value });
        if (nav.errorText) {
          throw new Error(`could not load ${args.value}: ${nav.errorText}`);
        }
        loading = true;
        await settle(0);
        break;
      }
      case 'click': {
        const at = await point();
        for (const type of ['mousePressed', 'mouseReleased']) {
          await cdp.send('Input.dispatchMouseEvent', { type, x: at.x, y: at.y, button: 'left', clickCount: 1 });
        }
        await settle(500);
        break;
      }
      case 'type': {
        await point();
        if (!(await evaluate(clearScript(args.ref)))) {
          throw new Error(`ref ${args.ref} is not on the page; take a snapshot and use one of its refs`);
        }
        await cdp.send('Input.insertText', { text: args.value });
        if (args.submit) {
          await key('Enter');
          await settle(500);
        }
        break;
      }
      case 'press':
        await key(args.key);
        await settle(300);
        break;
      case 'scroll':
        await evaluate(`window.scrollBy(0, ${args.direction === 'up' ? -1 : 1} * Math.round(window.innerHeight * 0.9))`);
        break;
      case 'back': {
        const history = await cdp.send('Page.getNavigationHistory');
        if (history.currentIndex < 1) {
          throw new Error('there is no page to go back to');
        }
        await cdp.send('Page.navigateToHistoryEntry', { entryId: history.entries[history.currentIndex - 1].id });
        loading = true;
        await settle(0);
        break;
      }
      default:
        break;
    }
    const snap = await evaluate(snapshotScript(args.maxChars));
    const page = { url: snap.url, title: snap.title, status, elements: snap.elements, more_elements: snap.more_elements,
      text: snap.text, truncated: snap.truncated };
    if (args.screenshot) {
      const shot = await cdp.send('Page.captureScreenshot', { format: 'jpeg', quality: 70 });
      const image = Buffer.from(shot.data, 'base64');
      writeFileSync(args.screenshot, image, { mode: 0o600 });
      page.screenshot_bytes = image.length;
    }
    return page;
  } finally {
    ws.close();
  }
}

// actMain runs a browser action on the session's running Chromium. A
// failure prints its reason on stdout, which is what the action returns,
// and exits 1.
async function actMain(argv) {
  let timer;
  let code = 0;
  try {
    const args = actArgs(argv);
    if (!(await waitFor(CDP_PORT, 2))) {
      throw new Error('the session browser is not running');
    }
    const late = new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`gregale-browser act ${args.kind}: no answer within ${args.timeout} s`)), args.timeout * 1000);
    });
    const page = await Promise.race([actOn(CDP_PORT, args), late]);
    process.stdout.write(JSON.stringify(page) + '\n');
  } catch (err) {
    console.log(err.message);
    code = 1;
  } finally {
    clearTimeout(timer);
  }
  process.exit(code);
}

async function main(argv) {
  if (argv[0] === 'act') {
    await actMain(argv.slice(1));
    return;
  }
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
