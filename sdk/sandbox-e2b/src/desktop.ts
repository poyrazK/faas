// Desktop: an E2B Desktop-style handle on a Gregale sandbox desktop
// (ADR-20261124075005 §6). Written from E2B Desktop's public documentation.
// Every action runs gregale-desktop in the session as an argv, never shell
// text, and the live view is noVNC behind the session's private preview
// ports.

import { Connection } from './connection.js';
import { CommandExitError, InvalidArgumentError } from './errors.js';
import { Sandbox, type SandboxOpts } from './sandbox.js';

/** The guest ports of the interactive and the view-only noVNC views. */
export const DESKTOP_VIEW_PORT = 6080;
export const DESKTOP_VIEW_ONLY_PORT = 6081;

const DISPLAY_ENV = { DISPLAY: ':0' };

export type DesktopOpts = Omit<SandboxOpts, 'template'> & {
  /** The screen, [width, height]; 1024x768 by default. */
  resolution?: [number, number];
};

export type MouseButton = 'left' | 'right' | 'middle';
export type ScrollDirection = 'up' | 'down' | 'left' | 'right';
export type ScreenshotFormat = 'bytes' | 'blob' | 'stream';

type ExecResponse = { stdout: string; stderr: string; exit_code: number; timed_out?: boolean; killed?: boolean };
type PortView = { port: number; url: string; preview_token?: string };

export type StreamUrlOpts = {
  /** Connect as soon as the page opens; true by default. */
  autoConnect?: boolean;
  /** The view-only port, whose server drops input; false by default. */
  viewOnly?: boolean;
  /** How the screen fits the page: scale (the default), remote or off. */
  resize?: 'scale' | 'remote' | 'off';
};

/** The noVNC live view: watch the desktop, or take it over. */
export class DesktopStream {
  readonly #conn: Connection;

  constructor(conn: Connection) {
    this.#conn = conn;
  }

  /**
   * Exposes both views privately. A desktop created with previews on has
   * them already; this brings them back after stop().
   */
  async start(_opts: { requireAuth?: boolean } = {}): Promise<void> {
    for (const port of [DESKTOP_VIEW_PORT, DESKTOP_VIEW_ONLY_PORT]) {
      await this.#conn.control('POST', '/ports', { json: { port, visibility: 'private' } });
    }
  }

  /**
   * The view's URL with its preview token, so a browser opens it once and
   * keeps a host-only cookie. Hand out viewOnly: true to people who should
   * only watch: that port's server takes no input, and its token opens no
   * other port.
   */
  async getUrl(opts: StreamUrlOpts = {}): Promise<string> {
    const port = opts.viewOnly ? DESKTOP_VIEW_ONLY_PORT : DESKTOP_VIEW_PORT;
    const view = await this.#conn.control<PortView>('GET', `/ports/${port}`);
    const url = new URL('/vnc.html', view.url);
    url.searchParams.set('autoconnect', opts.autoConnect === false ? '0' : '1');
    url.searchParams.set('resize', opts.resize ?? 'scale');
    url.searchParams.set('reconnect', '1');
    url.searchParams.set('path', 'websockify');
    if (opts.viewOnly) url.searchParams.set('view_only', '1');
    if (view.preview_token) url.searchParams.set('gregale_preview_token', view.preview_token);
    return url.toString();
  }

  /** The preview token of a view, for a caller that sends it as a bearer. */
  async getAuthKey(opts: { viewOnly?: boolean } = {}): Promise<string | undefined> {
    const port = opts.viewOnly ? DESKTOP_VIEW_ONLY_PORT : DESKTOP_VIEW_PORT;
    return (await this.#conn.control<PortView>('GET', `/ports/${port}`)).preview_token;
  }

  /** Unexposes both views; open viewers are cut off. */
  async stop(): Promise<void> {
    for (const port of [DESKTOP_VIEW_PORT, DESKTOP_VIEW_ONLY_PORT]) {
      await this.#conn.control('DELETE', `/ports/${port}`);
    }
  }
}

function coord(name: string, v: number): string {
  if (!Number.isInteger(v) || v < 0) throw new InvalidArgumentError(`${name} must be a non-negative integer; got ${v}`);
  return String(v);
}

/** A sandbox session with a desktop: Xfce on a virtual screen. */
export class Desktop extends Sandbox {
  readonly stream: DesktopStream;
  readonly #conn: Connection;

  /** Use Desktop.create or Desktop.connect. */
  constructor(conn: Connection, portVisibility: 'private' | 'public' = 'private') {
    super(conn, portVisibility);
    this.#conn = conn;
    this.stream = new DesktopStream(conn);
  }

  /** Creates a desktop session (Pro and Scale) and waits until it runs. */
  static override async create<S extends typeof Sandbox>(this: S, opts?: DesktopOpts): Promise<InstanceType<S>>;
  static override async create<S extends typeof Sandbox>(this: S, template: string, opts?: DesktopOpts): Promise<InstanceType<S>>;
  static override async create<S extends typeof Sandbox>(this: S, a?: string | DesktopOpts, b?: DesktopOpts): Promise<InstanceType<S>> {
    const opts: DesktopOpts = (typeof a === 'string' ? b : a) ?? {};
    const create = Sandbox.create as (this: S, opts: SandboxOpts) => Promise<InstanceType<S>>;
    return await create.call(this, { ...opts, template: 'desktop' });
  }

  /** Runs gregale-desktop with argv and answers its stdout. */
  async #run(args: string[], timeoutMs = 30_000): Promise<string> {
    const res = await this.#conn.json<ExecResponse>('POST', '/exec', {
      json: { cmd: ['/usr/local/bin/gregale-desktop', ...args], env: { ...this.#conn.envs, ...DISPLAY_ENV }, timeout_ms: timeoutMs },
    });
    if (res.exit_code !== 0) {
      const error = res.timed_out ? 'the command timed out' : res.stderr.trim() || undefined;
      throw new CommandExitError({ exitCode: res.exit_code, stdout: res.stdout, stderr: res.stderr, ...(error ? { error } : {}) });
    }
    return res.stdout;
  }

  /** A PNG of the screen: bytes (the default), a Blob or a stream. */
  screenshot(format?: 'bytes'): Promise<Uint8Array>;
  screenshot(format: 'blob'): Promise<Blob>;
  screenshot(format: 'stream'): Promise<ReadableStream<Uint8Array>>;
  async screenshot(format: ScreenshotFormat = 'bytes'): Promise<Uint8Array | Blob | ReadableStream<Uint8Array>> {
    const bytes = new Uint8Array(Buffer.from((await this.#run(['screenshot', '--base64'])).trim(), 'base64'));
    if (format === 'blob') return new Blob([bytes], { type: 'image/png' });
    if (format === 'stream') return new Response(bytes).body!;
    return bytes;
  }

  async #click(button: MouseButton, x?: number, y?: number, double = false): Promise<void> {
    if ((x === undefined) !== (y === undefined)) throw new InvalidArgumentError('pass both x and y, or neither');
    const at = x === undefined ? await this.getCursorPosition() : { x, y: y as number };
    await this.#run(['click', coord('x', at.x), coord('y', at.y), '--button', button, ...(double ? ['--double'] : [])]);
  }

  /** Clicks at (x, y), or where the pointer is. */
  async leftClick(x?: number, y?: number): Promise<void> {
    await this.#click('left', x, y);
  }

  async rightClick(x?: number, y?: number): Promise<void> {
    await this.#click('right', x, y);
  }

  async middleClick(x?: number, y?: number): Promise<void> {
    await this.#click('middle', x, y);
  }

  async doubleClick(x?: number, y?: number): Promise<void> {
    await this.#click('left', x, y, true);
  }

  async moveMouse(x: number, y: number): Promise<void> {
    await this.#run(['move', coord('x', x), coord('y', y)]);
  }

  async mousePress(button: MouseButton = 'left'): Promise<void> {
    await this.#run(['down', '--button', button]);
  }

  async mouseRelease(button: MouseButton = 'left'): Promise<void> {
    await this.#run(['up', '--button', button]);
  }

  /** Presses at from, moves, releases at to. */
  async drag(from: [number, number], to: [number, number]): Promise<void> {
    await this.#run(['drag', coord('x', from[0]), coord('y', from[1]), coord('x', to[0]), coord('y', to[1])]);
  }

  async scroll(direction: ScrollDirection = 'down', amount = 1): Promise<void> {
    await this.#run(['scroll', direction, String(amount)]);
  }

  /**
   * Types text into the focused window. Long text goes in chunks of
   * chunkSize characters, delayInMs apart.
   */
  async write(text: string, opts: { chunkSize?: number; delayInMs?: number } = {}): Promise<void> {
    const size = Math.max(1, opts.chunkSize ?? 4096);
    for (let i = 0; i < text.length; i += size) {
      if (i > 0 && opts.delayInMs) await new Promise((r) => setTimeout(r, opts.delayInMs));
      await this.#run(['type', '--', text.slice(i, i + size)], 120_000);
    }
  }

  /** Presses a key ("enter"), or a chord as an array (["ctrl", "c"]). */
  async press(key: string | string[]): Promise<void> {
    await this.#run(['key', '--', Array.isArray(key) ? key.join('+') : key]);
  }

  async getCursorPosition(): Promise<{ x: number; y: number }> {
    const [x, y] = (await this.#run(['cursor'])).trim().split(/\s+/).map(Number);
    return { x: x ?? 0, y: y ?? 0 };
  }

  async getScreenSize(): Promise<{ width: number; height: number }> {
    const [width, height] = (await this.#run(['size'])).trim().split('x').map(Number);
    return { width: width ?? 0, height: height ?? 0 };
  }

  /** Starts an application (chromium, xfce4-terminal, mousepad, …), optionally on a URI. */
  async launch(application: string, uri?: string): Promise<void> {
    await this.#run(['launch', application, ...(uri ? ['--', uri] : [])]);
  }

  /** Opens a file or URL with its default application. */
  async open(fileOrUrl: string): Promise<void> {
    await this.#run(['open', '--', fileOrUrl]);
  }

  async #windows(app?: string): Promise<{ id: string; app: string; title: string }[]> {
    const out = await this.#run(['windows', ...(app ? ['--app', app] : [])]);
    return out
      .split('\n')
      .filter((l) => l.trim() !== '')
      .map((l) => {
        const [id = '', cls = '', ...title] = l.split('\t');
        return { id, app: cls, title: title.join('\t') };
      });
  }

  /** The IDs of an application's windows. */
  async getApplicationWindows(application: string): Promise<string[]> {
    return (await this.#windows(application)).map((w) => w.id);
  }

  async getCurrentWindowId(): Promise<string> {
    return (await this.#run(['window'])).split('\t')[0]?.trim() ?? '';
  }

  async getWindowTitle(windowId: string): Promise<string> {
    const w = (await this.#windows()).find((v) => v.id === windowId);
    if (w) return w.title;
    const [id, , title = ''] = (await this.#run(['window'])).replace(/\n$/, '').split('\t');
    if (id === windowId) return title;
    throw new InvalidArgumentError(`no window ${windowId}`);
  }

  /** Waits ms milliseconds. */
  async wait(ms: number): Promise<void> {
    await new Promise((r) => setTimeout(r, ms));
  }
}
