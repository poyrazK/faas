# ADR-20261124075005 · Sandbox desktops: a computer-use API and a live view

- **Status:** proposed (owner decisions, 2026-11-24: scope "Agent API + live view", desktop "Xfce", stream "noVNC over x11vnc", plans "Pro and Scale"; then RAM minimum 1536 MB, a platform desktop drive shared by every account, and warm desktop snapshots captured prewarmed and stopped)
- **Date:** 2026-11-24
- **Amends:** ADR-20261027090013 §1 (a fourth platform preset, `desktop`), §7 (new limits), §12 (an E2B Desktop shim).
- **Builds on:** ADR-20261027090013 step 14 (preset `code-interpreter` boots a platform image), ADR-20261027090013 templates (start and ready commands), ADR-20261116182615 (browser sessions, private DevTools port), ADR-20261122132055 (persistent sessions, pause, fork), ADR-003 (untrusted code runs only in microVMs).

## Context

Computer-use agents (Claude computer use, OpenAI CUA, browser-use and others) drive a whole desktop: they take a screenshot, decide, and click, type or scroll at pixel coordinates. E2B Desktop, Daytona and Scrapybara sell this as a sandbox with a desktop, an API for those actions, and a stream a person can watch and take over. Gregale sessions today have a headless browser (CDP, ADR-20261116182615) but no screen.

A desktop is just more programs in the guest: an X server, a window manager, a VNC server and the apps. The VM boundary, the jail, egress policy, freeze, pause, fork and billing work unchanged. The cost is memory: Xvfb, Xfce and x11vnc idle near 300 MB, and with Chromium open a desktop sits near 1 GB. That is why the preset needs at least 1.5 GB and is on Pro and Scale only.

## Decision

### 1. Preset `desktop`

- `POST /v1/sandboxes` with `preset: "desktop"` creates an image session that boots the platform desktop drive (§3a), not a per-account copy of the image. `FAAS_SANDBOX_DESKTOP_IMAGE` is the image's digest-pinned reference, which the operator sets for apid and for the agent pool's imaged. Unset, the preset is `sandbox_unsupported`; a reference without a digest keeps apid from starting.
  - The preset takes no `image`, `template`, `pull`, `registry_auth` or `browser`. The desktop image has Chromium; the `browser` flag is for the headless agent-base browser.
  - It works with `kind: "persistent"` (pause, resume, fork keep the open windows) and with every network mode. A desktop without `web` network can still be driven; its browser just cannot reach the internet.
- `resolution: "WIDTHxHEIGHT"` sets the screen. The default is `SandboxDesktopDefaultResolution` (1024x768: computer-use models are trained on and scale screenshots to about this size). Width is 640–`SandboxDesktopWidthMax` (1920), height 480–`SandboxDesktopHeightMax` (1080). It is refused on any other preset.
- RAM defaults to `SandboxDesktopDefaultRAMMB` (2048) and may not go below `SandboxDesktopRAMMBMin` (1536). An ephemeral session gets the plan's `AgentRunVCPUMax` vCPUs (2 on Pro and Scale); a persistent one may ask for more.
- **Plans:** `SandboxDesktopEnabled` is false on Free and Hobby. A create there is a limit error.

### 2. The session row and who starts the desktop

- `sandbox_sessions.desktop_resolution` (text, `NULL` for any other session, `CHECK` on the `WxH` form) records a desktop session and its screen. `sandbox_sessions.desktop_image_key` (text, `NULL` for any other session, `CHECK` on the platform key's form) records the drive it boots, and `image_ref` the image's reference, for the session's view. A desktop session has no image-run job: it is claimable at once, it never fails as `image_unavailable`, and the claim's image key is the platform key. A fork copies both columns. The migration is expand-only.
- agentd turns it into the session's start and ready commands, as a template's are: `gregale-desktop start --resolution WxH` and `gregale-desktop wait`, ready within `SandboxDesktopReadyTimeoutSec` (45 s). The commands are agentd's constants; the only caller input is the resolution, which apid validated and the column's `CHECK` holds to digits. They travel as the existing `start_cmd` and `ready_cmd` fields, so schedd and vmmd do not change and an older schedd or vmmd starts a desktop correctly.
- A cold boot runs them from the staged manifest. A restore from a warm snapshot runs them by `template_start`. A resume of a pause capture runs none of them: the desktop was running when it was captured.

### 3. The desktop image

- `images/sandbox-desktop.Dockerfile`, Debian slim, published by digest like the code-interpreter image. It carries:
  - Xvfb (the screen, 24-bit), Xfce (panel, window manager, file manager, terminal), D-Bus.
  - x11vnc and noVNC with websockify.
  - xdotool (input), wmctrl (windows), xclip, ImageMagick (JPEG screenshots). PNG screenshots are read from the X server by `gregale-desktop` itself (§4).
  - Chromium, fonts (DejaVu, Liberation, Noto, emoji).
  - `gregale-desktop`, the one command the platform and agents use (§4).
- Everything runs as the session's user, uid 1000. Nothing in the image is setuid. The X display is `:0`. guest-init gives commands its own environment, not the image's, so `gregale-desktop` always targets `:0`, `/etc/profile.d` sets `DISPLAY=:0` for login shells and terminals, and the SDKs pass `DISPLAY=:0` with the commands they run. A customer's own command passes it the same way.

### 3a. One platform drive for every account

Image sessions boot a per-account copy of their image: the account's image-run job pulls it and imaged materializes it under `jobs/<job>`. For the desktop that cost each account's first desktop a 1.67 GB pull, took one of the account's `ImageRunCachedImages`, and failed on Pro, whose full-rootfs cap (`MaxFullRootfsLayerBytes`, 1 GB) is below the desktop's 1.2 GB root. And since a warm snapshot restores only onto the exact drive it was captured with, no snapshot could serve two accounts.

- The agent pool's imaged stages the desktop image once per release of the image, as it stages the agent base: when `FAAS_SANDBOX_DESKTOP_IMAGE` is set, it pulls the image by digest and publishes it as a full-rootfs drive at `base/sandbox-desktop-<manifest digest>.ext4` (`storagekey.SandboxDesktopImage`), unless that key is already in the shared store. It builds the drive with Scale's full-rootfs cap: the image is the platform's, not a customer's. The key names the image's content, so a new image gets a new key and the old one is never overwritten.
- apid derives the same key from the same reference and records it on the session (§2). vmmd boots a session's image drive only if it is a job image, a template build or a platform desktop drive.
- Every account's desktop boots the same read-only drive, as every preset session boots the agent base. It holds only the platform's image.
- Rollout order: the image reference goes on the agent pool's imaged first, then on apid once the drive is staged. A desktop that starts before its drive is in the shared store fails to wake and is retried as any failed wake is.

### 3b. Warm desktop snapshots

A desktop restores from a warm snapshot captured as the browser shape's is (ADR-20261116182615 §2): prewarmed, then stopped.

- schedd keeps one desktop slot per node and machine shape (vCPUs, RAM, network mode), shared by every account. Its keys are in the `sandbox-desktop` namespace, which vmmd restores only into a session that boots a platform desktop drive. As for every warm snapshot, the capture follows a shape's first clean cold boot on the node, and the restore needs the exact drive the capture holds.
- The capture boots the session machine with no session data, waits for guest-init, and runs `gregale-desktop prewarm` as the session's user. Prewarm starts the desktop at the default resolution, waits until it is ready, starts and closes Chromium once, then stops every desktop process and removes the X server's socket, lock and logs. The VM is captured after that. It holds no running desktop process, only the kernel's page cache of the files those processes read, so every process of a restored desktop starts after the restore reseeds the kernel's RNG and steps the clock (ADR-171, invariant 5), exactly as on a cold boot.
- A restored desktop runs its start and ready commands by `template_start`, so one capture serves every resolution.
- Rejected: capturing the running desktop at the default resolution. It would restore ready in well under a second, but Xvfb, x11vnc, websockify and Xfce would share pre-capture memory and userspace random state across every account's sessions, which ADR-171 does not allow (owner decision, 2026-11-24).

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
- `screenshot` reads the root window with one X11 `GetImage` over the X server's unix socket and encodes the PNG with zlib level 1, in the standard library. It falls back to ImageMagick's `import` when the screen's pixels are not 32-bit xRGB. A click is a press and a release 20 ms apart; xdotool's `click` holds the button for 100 ms. `move` and `click` do not use xdotool's `mousemove --sync`, which never returns when the pointer is already where it is sent.

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
| Cold boot to ready (start and ready commands) | 5.3 s (8.1–9.2 s through `xfce4-session`) |
| VM resident memory, idle desktop | 497 MB (689 MB through `xfce4-session`) |
| VM resident memory, Chromium and Mousepad open | 923 MB (1,400 MB through `xfce4-session`) |
| `move` + `cursor` round trip through exec | 95–182 ms |
| Capture of the running desktop | 7.3–8.7 s; memory file 2,048 MB (the guest's size, sparse) |
| Restore of the capture to a working desktop (windows kept) | 2.3–2.6 s |
| VM resident memory after the restore | 465 MB |

Capture and restore are from runs with the work directory on the host's NVMe drive; on its EBS root disk they took 31 s and 6 s.

The warm desktop snapshot (§3b), with the work directory on the EBS root disk (the NVMe drive was full) and a 4 GB test drive holding the 1.2 GB root:

| What | Result |
|---|---|
| Prewarm in Docker (desktop up, Chromium opened and closed, all stopped) | 2.3 s; a start after it is ready in 0.5 s |
| Warm capture: cold boot, prewarm, snapshot | 42–50 s, of which staging the writable drive copy 9.6 s and publishing the drive with the snapshot 19 s |
| Warm restore to a ready desktop at a new resolution | 10.4–10.7 s, of which the host's restore 9.2 s and the guest's resume hook 0.1 s |
| First Chromium window after the warm restore | 1.2–1.3 s |
| VM resident memory after the warm restore | 412–421 MB |

On this disk the warm restore is slower than the 5.1 s cold boot. The time is the host copying the drive the capture couples (a capture keeps the drive it was taken with, since the guest's page cache describes it), not the guest. The drive copy scales with the disk, as for the fork capture above. Before warm snapshots are turned on for desktops, the restore must be measured with the work directory on NVMe and a drive imaged sizes to the root (see Not yet).

The image is 1.75 GB (1.2 GB unpacked root). In Docker on the same host the desktop is ready in 0.9–1.6 s.

`gregale-desktop start` runs Xfce's parts (xfsettingsd, xfwm4, xfdesktop, xfce4-panel) at once under one D-Bus session instead of through `xfce4-session`. A boot breakdown in the metal test showed `xfce4-session` starting them one by one, with the panel appearing 5.5 s after Xvfb on a cold guest. It also started the session's autostart extras, which cost about 190 MB idle. With the direct start the guest is ready about 2 s after boot, and the rest of the cold boot is Firecracker bringing the VM up and reading the image drive. `wait` requires xfdesktop's and the panel's windows to be mapped, since with everything starting at once a window can exist before it is on screen. A desktop should still resume from a pause capture or warm snapshot where it can. 2048 MB stays the default: it leaves room for a second browser or a heavier page. The minimum is 1536 MB (2048 MB until these measurements): Chromium's 923 MB leaves about 600 MB of headroom, and since a session is billed on its RAM, a desktop at the minimum costs a quarter less.

### Limits (`pkg/api/limits.go`)

| Limit | Free | Hobby | Pro | Scale |
|---|---:|---:|---:|---:|
| `SandboxDesktopEnabled` | no | no | yes | yes |
| `SandboxDesktopRAMMBMin` | 1536 | 1536 | 1536 | 1536 |
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
5. The platform drive (§3a) and warm desktop snapshots (§3b): imaged's staging, the session column, vmmd's allow-list and namespace, schedd's shared slot and the prewarm. Warm snapshots stay behind `FAAS_SCHEDD_SANDBOX_SNAPSHOTS`, as for every session.

## Rejected alternatives

- **KasmVNC, Selkies or WebRTC streaming.** Lower latency and better video, but each is a larger server in the guest, and WebRTC needs TURN through the egress gateway. noVNC is enough to watch and take over; computer-use agents read screenshots, not the stream.
- **A desktop flag on any image.** Customers' images would need X, a window manager and VNC at fixed paths. A platform image is one tested thing; customers who need their own packages install them in the session or build a template from the desktop image later.
- **New action routes and wire frames, like `gregale_browser`.** SDK users already hold a session and run commands; an argv command adds no surface and works through every existing path (exec, pause, fork). A stateless-agent tool on top can come later and reuse `gregale-desktop`.
- **A VNC password.** The preview token already authenticates every request and is per port; a second secret adds nothing.
- **GNOME or KDE.** Each idles at 600 MB or more and starts slower; Xfce does what an agent needs.

## Not yet

- A warm desktop restore that does not copy the drive: boot the platform drive read-only with the session's writes in an overlay on a per-session layer, so a capture couples only that small layer. That changes the image session's drive layout and needs its own decision. Until warm restores beat the cold boot on the reference node, `FAAS_SCHEDD_SANDBOX_SNAPSHOTS` stays off where desktops run.

- Audio, clipboard sync with the viewer, file drag and drop into the view.
- Several displays per session, GPU rendering.
- A `gregale_desktop` tool for stateless agents.
- Recording a session to video.
