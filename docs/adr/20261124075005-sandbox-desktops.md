# ADR-20261124075005 · Sandbox desktops: a computer-use API and a live view

- **Status:** proposed (owner decisions, 2026-11-24: scope "Agent API + live view", desktop "Xfce", stream "noVNC over x11vnc", plans "Pro and Scale")
- **Date:** 2026-11-24
- **Amends:** ADR-20261027090013 §1 (a fourth platform preset, `desktop`), §7 (new limits), §12 (an E2B Desktop shim).
- **Builds on:** ADR-20261027090013 step 14 (preset `code-interpreter` boots a platform image), ADR-20261027090013 templates (start and ready commands), ADR-20261116182615 (browser sessions, private DevTools port), ADR-20261122132055 (persistent sessions, pause, fork), ADR-003 (untrusted code runs only in microVMs).

## Context

Computer-use agents (Claude computer use, OpenAI CUA, browser-use and others) drive a whole desktop: they take a screenshot, decide, and click, type or scroll at pixel coordinates. E2B Desktop, Daytona and Scrapybara sell this as a sandbox with a desktop, an API for those actions, and a stream a person can watch and take over. Gregale sessions today have a headless browser (CDP, ADR-20261116182615) but no screen.

A desktop is just more programs in the guest: an X server, a window manager, a VNC server and the apps. The VM boundary, the jail, egress policy, freeze, pause, fork and billing work unchanged. The cost is memory: Xvfb, Xfce and x11vnc idle near 300 MB, and with Chromium open a desktop sits near 1 GB. That is why the preset needs 2 GB and is on Pro and Scale only.

## Decision

### 1. Preset `desktop`

- `POST /v1/sandboxes` with `preset: "desktop"` creates an image session of the platform desktop image. As with `code-interpreter`, apid rewrites the preset to `image: <FAAS_SANDBOX_DESKTOP_IMAGE>`, a digest-pinned reference the operator sets. Unset, the preset is `sandbox_unsupported`.
  - The preset takes no `image`, `template`, `pull`, `registry_auth` or `browser`. The desktop image has Chromium; the `browser` flag is for the headless agent-base browser.
  - It works with `kind: "persistent"` (pause, resume, fork keep the open windows) and with every network mode. A desktop without `web` network can still be driven; its browser just cannot reach the internet.
- `resolution: "WIDTHxHEIGHT"` sets the screen. The default is `SandboxDesktopDefaultResolution` (1024x768: computer-use models are trained on and scale screenshots to about this size). Width is 640–`SandboxDesktopWidthMax` (1920), height 480–`SandboxDesktopHeightMax` (1080). It is refused on any other preset.
- RAM defaults to `SandboxDesktopDefaultRAMMB` (2048) and may not go below `SandboxDesktopRAMMBMin` (2048). An ephemeral session gets the plan's `AgentRunVCPUMax` vCPUs (2 on Pro and Scale); a persistent one may ask for more.
- **Plans:** `SandboxDesktopEnabled` is false on Free and Hobby. A create there is a limit error.

### 2. The session row and who starts the desktop

- `sandbox_sessions.desktop_resolution` (text, `NULL` for any other session, `CHECK` on the `WxH` form) records a desktop session and its screen. The migration is expand-only.
- agentd turns it into the session's start and ready commands, as a template's are: `gregale-desktop start --resolution WxH` and `gregale-desktop wait`, ready within `SandboxDesktopReadyTimeoutSec` (45 s). The commands are agentd's constants; the only caller input is the resolution, which apid validated and the column's `CHECK` holds to digits. They travel as the existing `start_cmd` and `ready_cmd` fields, so schedd and vmmd do not change and an older schedd or vmmd starts a desktop correctly.
- A cold boot runs them from the staged manifest. A restore from a warm snapshot runs them by `template_start`. A resume of a pause capture runs none of them: the desktop was running when it was captured.

### 3. The desktop image

- `images/sandbox-desktop.Dockerfile`, Debian slim, published by digest like the code-interpreter image. It carries:
  - Xvfb (the screen, 24-bit), Xfce (panel, window manager, file manager, terminal), D-Bus.
  - x11vnc and noVNC with websockify.
  - xdotool (input), ImageMagick `import` (screenshots), wmctrl (windows), xclip.
  - Chromium, fonts (DejaVu, Liberation, Noto, emoji).
  - `gregale-desktop`, the one command the platform and agents use (§4).
- Everything runs as the session's user, uid 1000. Nothing in the image is setuid. The X display is `:0`. guest-init gives commands its own environment, not the image's, so `gregale-desktop` always targets `:0`, `/etc/profile.d` sets `DISPLAY=:0` for login shells and terminals, and the SDKs pass `DISPLAY=:0` with the commands they run. A customer's own command passes it the same way.

### 4. The agent API: `gregale-desktop`

- Agents act with the ordinary exec operation and an argv array, never shell text:

  | command | does |
  |---|---|
  | `screenshot [--format png\|jpeg] [--quality N]` | writes the screen to stdout (PNG by default) |
  | `click X Y [--button left\|right\|middle] [--double]` | moves to (X, Y) and clicks |
  | `move X Y` | moves the pointer |
  | `down` / `up [--button B]` | presses or releases a button |
  | `drag X1 Y1 X2 Y2` | presses at the first point, moves, releases at the second |
  | `scroll up\|down\|left\|right [AMOUNT]` | scrolls AMOUNT notches (default 1) at the pointer |
  | `type -- TEXT` | types TEXT into the focused window |
  | `key KEYS...` | presses a key or a chord (`ctrl+c`, `Return`) |
  | `cursor` | prints the pointer position as `X Y` |
  | `size` | prints the screen size as `WIDTHxHEIGHT` |
  | `launch APP [-- ARGS...]` | starts an application in the background (`chromium`, `xfce4-terminal`, `mousepad`, or any program) |
  | `open TARGET` | opens a file or URL with its default application |
  | `windows [--app APP]` | lists windows: id, title |
  | `window` | prints the focused window's id and title |
  | `start --resolution WxH` / `wait` | the session's start and ready commands (§2) |

- Coordinates must be inside the screen, keys must be xdotool key names, and `type` text is at most `SandboxDesktopTypeMaxChars` (8192); anything else exits 2 with a message. `type` text goes to xdotool as one argv entry after `--`.
- There is no new route and no new wire message. The SDKs (§6) wrap these commands.

### 5. The live view

- The session exposes two private preview ports when previews are on, as a browser session exposes 9222:
  - **6080, interactive:** noVNC against an x11vnc that takes input. A person can watch and take over.
  - **6081, view only:** noVNC against a second x11vnc started with `-viewonly`. The server drops input, so the link is safe to share with someone who should only watch.
- Access is the private preview token (`?gregale_preview_token=` once in a browser, then the cookie). x11vnc has no password: the token is the gate, and the VNC servers listen only inside the guest. A token is per port, so a viewer given the 6081 token cannot use 6080.
- The two ports count against `SandboxExposedPortsMax`; with fewer than two free, the create is a limit error naming it.
- The stream is VNC: it sends only changed tiles, so an idle desktop costs almost nothing, and the session still freezes when no call and no viewer is active. An open view is an open preview connection, which keeps the session from freezing as any open preview connection does; closing the tab lets it freeze.

### 6. SDKs

- `@gregale/sandbox-e2b` and `gregale-sandbox-e2b` gain a `Desktop` class shaped like E2B Desktop's `Sandbox`: `create({resolution})`, `screenshot()`, `leftClick`, `rightClick`, `middleClick`, `doubleClick`, `moveMouse`, `press`/`release`, `drag`, `scroll`, `write`, `press(keys)`, `getCursorPosition`, `getScreenSize`, `launch`, `open`, `getApplicationWindows`, `getCurrentWindowId`, `getWindowTitle`, `wait`, and `stream.start()`, `stream.getUrl({viewOnly})`, `stream.stop()`. `stream.start` exposes nothing new: the ports already exist. `getUrl` returns the noVNC page on 6080 or 6081 with the port's preview token.
- Everything goes through `commands.run` with an argv, so the shim adds no transport.

### 7. Billing and capacity

- A desktop is billed as any sandbox session: RAM + 8 MB per running second (§4.7). Frozen and paused desktops cost what any frozen or paused session costs.
- Invariant 2 is unchanged: the session admits at its RAM like any other.
- A desktop's snapshots are larger than a headless session's (its touched memory is 0.6–1.2 GB). Warm snapshots of the desktop image and pause captures are sandbox snapshots, kept apart from the apps' `snapshot_fleet_avg_mb`.

### Measured (2026-11-24, `TestMetalSandboxDesktop`)

On the m7i-flex test host (nested KVM, Firecracker 1.10, 2 vCPU, 2048 MB, 1280x800, network `none`):

| What | Result |
|---|---|
| Cold boot to ready (start and ready commands) | 9.2 s |
| VM resident memory, idle desktop | 689 MB |
| VM resident memory, Chromium and Mousepad open | 1,400 MB |
| `move` + `cursor` round trip through exec | 182 ms |
| Capture of the running desktop | 7.3 s; memory file 2,048 MB (the guest's size, sparse) |
| Restore of the capture to a working desktop (windows kept) | 2.3 s |
| VM resident memory after the restore | 596 MB |

The image is 1.75 GB (1.2 GB unpacked root). In Docker on the same host the desktop is ready in 0.8–1.6 s, so most of the VM's cold boot is reading the image drive and starting Xfce cold. That is why a desktop should resume from a pause capture or warm snapshot where it can. 2048 MB is the right default: Chromium takes the VM to about 1.4 GB.

### Limits (`pkg/api/limits.go`)

| Limit | Free | Hobby | Pro | Scale |
|---|---:|---:|---:|---:|
| `SandboxDesktopEnabled` | no | no | yes | yes |
| `SandboxDesktopRAMMBMin` | 2048 | 2048 | 2048 | 2048 |
| `SandboxDesktopDefaultRAMMB` | 2048 | 2048 | 2048 | 2048 |
| `SandboxDesktopDefaultResolution` | 1024x768 | | | |
| `SandboxDesktopWidthMax` × `SandboxDesktopHeightMax` | 1920 × 1080 | | | |
| `SandboxDesktopReadyTimeoutSec` | 45 | | | |
| `SandboxDesktopTypeMaxChars` | 8192 | | | |

## Rollout

1. This ADR, the limits, the migration, apid's preset and agentd's commands. Dark until `FAAS_SANDBOX_DESKTOP_IMAGE` is set.
2. The image and `gregale-desktop`, with unit tests of the argument checks and an image smoke test in CI that starts the desktop under Xvfb and takes a screenshot.
3. The SDK `Desktop` classes, docs (`docs/sandbox-migration.md`, a desktop guide).
4. A metal check on a native KVM host: cold boot to ready, warm restore to ready, a click and a type round trip, a screenshot, pause and resume with a window open, and the RSS and snapshot sizes. The numbers go into this ADR before the operator sets the image on prod.

## Rejected alternatives

- **KasmVNC, Selkies or WebRTC streaming.** Lower latency and better video, but each is a larger server in the guest, and WebRTC needs TURN through the egress gateway. noVNC is enough to watch and take over; computer-use agents read screenshots, not the stream.
- **A desktop flag on any image.** Customers' images would need X, a window manager and VNC at fixed paths. A platform image is one tested thing; customers who need their own packages install them in the session or build a template from the desktop image later.
- **New action routes and wire frames, like `gregale_browser`.** SDK users already hold a session and run commands; an argv command adds no surface and works through every existing path (exec, pause, fork). A stateless-agent tool on top can come later and reuse `gregale-desktop`.
- **A VNC password.** The preview token already authenticates every request and is per port; a second secret adds nothing.
- **GNOME or KDE.** Each idles at 600 MB or more and starts slower; Xfce does what an agent needs.

## Not yet

- Audio, clipboard sync with the viewer, file drag and drop into the view.
- Several displays per session, GPU rendering.
- A `gregale_desktop` tool for stateless agents.
- Recording a session to video.
