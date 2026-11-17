# ADR-20261112133503 · The sandbox browser lives in agent-base

- **Status:** proposed (owner decision, 2026-11-12: Chromium "in agent-base")
- **Date:** 2026-11-12
- **Amends (when accepted):** ADR-20261027090014 §4.3 (the opt-in `browser` preset layer and `web: browser`), §4.7 (`AgentBrowserAllowed`), and open question 4 (the browser's size on drive0).
- **Builds on:** ADR-20261027090010 §2 (agent-base, the shared drive0), ADR-20261027090013 §11 (exposed ports and preview URLs), ADR-20261112121456 (sandbox `web` network mode).

## Context

Every agent sandbox provider offers a headless browser that agent frameworks drive over the Chrome DevTools Protocol (CDP): Playwright's `connectOverCDP`, Puppeteer's `connect`, browser-use. ADR-20261027090014 §4.3 planned an opt-in `browser` preset layer for Pro and above, and left its drive0 size open.

Every preset session and agent run boots the same read-only agent-base drive0, staged once per agent node, so a binary there costs node disk once rather than per session. Chromium's resident memory is spent only by a session that starts it.

## Decision

1. **Chromium, playwright-core and Playwright MCP are in agent-base** (owner decision). Alpine's Chromium with its fonts adds about 300 MB to drive0 once per node. Every preset session and agent run can start a browser; there is no separate preset layer and no `web: browser` flag.
2. **`gregale-browser` starts it.** It runs headless Chromium with CDP on `127.0.0.1:9221` and serves CDP on `127.0.0.1:9222` through a relay. A preview request keeps its preview host as `Host`, which Chromium refuses for DevTools requests, so the relay sends Chromium its own host and rewrites the WebSocket URLs Chromium answers with to the caller's (`wss://` through a preview URL). It uses only Node built-ins.
3. **Customers reach it through a private exposed port.** `GregaleSandboxSession.startBrowser()` (`@gregale/agents-sandbox`) starts the browser if it is not running, exposes port 9222 privately, and returns the preview URL with an `Authorization` header carrying the preview token. CDP is full control of the browser, so the SDK never exposes it publicly.
4. **Plans.** Any plan with sandbox sessions may start the browser; `AgentBrowserAllowed` is dropped. The session's RAM bounds it: Chromium needs about 1 GB for ordinary pages, so the documented minimum is `ram_mb: 1024`, and 2048 for heavy pages. Pages reach the web only in `web` mode (ADR-20261112121456), which carries the egress controls; in `none` or `registries` mode the browser drives pages the session serves itself.
5. **Supply chain.** The npm packages are pinned in `images/agent-base/package.json` and its integrity-pinned lockfile. None has an install script, so no browser is downloaded at build time. The image's high-vulnerability scan covers Chromium like every other package.

## Consequences

- agent-base grows by about 300 MB. Nodes restage drive0 once on the next agent-base release.
- A Chromium security fix ships as an agent-base release, on the image pipeline's cadence.
- The Chromium sandbox is off (`--no-sandbox`): the microVM is the boundary, and the guest has no user namespaces for it.
- Not yet: a warm snapshot with the browser already started, a `gregale_browse` RAP tool for stateless agents, and `web.screenshot` (ADR-20261027090018).

## Rejected alternatives

- **A separate browser image booted as an image session.** It keeps agent-base lean, but each account caches its own copy against its image cache, and it needs its own publish pipeline and preset start commands.
- **Exposing Chromium's CDP port directly.** Chromium refuses the preview host, and its WebSocket URLs name `127.0.0.1`.
