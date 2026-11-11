// GregaleSandboxClient: an OpenAI Agents SDK SandboxClient whose sessions
// are Gregale sandbox sessions (ADR-20261027090013 §8). The model call
// stays in the developer's process; commands and files run in a microVM on
// Gregale.

import {
  Manifest,
  SandboxConfigurationError,
  SandboxLifecycleError,
  SandboxUnsupportedFeatureError,
  isNoopSnapshotSpec,
  normalizeSandboxClientCreateArgs,
  type SandboxClient,
  type SandboxClientCreateArgs,
  type SandboxClientOptions,
  type SandboxClientResumeOptions,
  type SandboxSessionResumeValidationInput,
} from '@openai/agents-core/sandbox';
import { GregaleApiError, SandboxApi, type ApiOptions, type SandboxView } from './api.js';
import { GregaleSandboxSession, validateManifest, type GregaleSandboxSessionState, type GregaleSessionOptions } from './session.js';

export interface GregaleSandboxClientOptions extends SandboxClientOptions {
  /** The harness preset: codex (default), opencode or claude-code. */
  preset?: string;
  /** VM memory, a multiple of 512 within the plan's AgentRunRAMMBMax. */
  ramMb?: number;
  /** Egress: none, registries (default: package registries over 443) or web. */
  network?: 'none' | 'registries' | 'web';
  /** Idle seconds before the session freezes, within the plan's limits. */
  idleTimeoutS?: number;
  /** Seconds the session may live, within SandboxMaxLifetimeSec. */
  lifetimeS?: number;
  labels?: Record<string, string>;
  /** A platform tenant to run the session for. */
  tenant?: string;
  /** Start from a workspace persisted with persistSnapshot(). */
  snapshotId?: string;
  /** Visibility of ports resolveExposedPort exposes; private by default. */
  portVisibility?: 'private' | 'public';
  /** How long create and resume wait for the VM; 120 s by default. */
  readyTimeoutMs?: number;
}

const SANDBOX_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export class GregaleSandboxClient implements SandboxClient<GregaleSandboxClientOptions, GregaleSandboxSessionState> {
  readonly backendId = 'gregale';
  readonly #api: SandboxApi;
  readonly #defaults: GregaleSandboxClientOptions;

  constructor(options: GregaleSandboxClientOptions & ApiOptions = {}) {
    const { apiKey, apiBase, fetch, ...defaults } = options;
    this.#api = new SandboxApi({ apiKey, apiBase, fetch });
    this.#defaults = defaults;
  }

  get apiBase(): string {
    return this.#api.apiBase;
  }

  async create(
    args?: SandboxClientCreateArgs<GregaleSandboxClientOptions> | Manifest,
    manifestOptions?: GregaleSandboxClientOptions,
  ): Promise<GregaleSandboxSession> {
    const normalized = normalizeSandboxClientCreateArgs(args, manifestOptions);
    const options = { ...this.#defaults, ...normalized.options };
    if (normalized.snapshot && !isNoopSnapshotSpec(normalized.snapshot)) {
      throw new SandboxUnsupportedFeatureError(
        `Gregale sessions do not take ${normalized.snapshot.type} snapshots; persist with session.persistSnapshot() and create with options.snapshotId`,
        { type: normalized.snapshot.type },
      );
    }
    // Refuse what cannot be built before a VM is started for it.
    validateManifest(normalized.manifest);
    const environment = await normalized.manifest.resolveEnvironment();
    const view = await this.#api.control<SandboxView>('POST', '/v1/sandboxes', {
      json: {
        preset: options.preset ?? 'codex',
        ram_mb: options.ramMb,
        network: options.network ? { mode: options.network } : undefined,
        idle_timeout_s: options.idleTimeoutS,
        lifetime_s: options.lifetimeS,
        labels: options.labels,
        tenant: options.tenant,
        snapshot_id: options.snapshotId,
      },
    });
    const session = this.#session(
      { backendId: 'gregale', sandboxId: view.id, apiBase: this.#api.apiBase, manifest: normalized.manifest, environment, workspaceReady: false },
      options,
    );
    try {
      await session.waitReady();
      await session.applyManifest(normalized.manifest);
      session.state.workspaceReady = true;
    } catch (err) {
      await session.delete().catch(() => undefined);
      throw err;
    }
    return session;
  }

  async resume(
    state: GregaleSandboxSessionState,
    options?: SandboxClientResumeOptions<GregaleSandboxClientOptions>,
  ): Promise<GregaleSandboxSession> {
    this.#checkState(state);
    const session = this.#session(
      { ...state, backendId: 'gregale', environment: await state.manifest.resolveEnvironment() },
      { ...this.#defaults, ...options?.clientOptions },
    );
    try {
      await session.waitReady();
    } catch (err) {
      if (err instanceof GregaleApiError && err.status === 404) {
        throw new SandboxLifecycleError(`Gregale sandbox ${state.sandboxId} no longer exists`, { sandboxId: state.sandboxId });
      }
      throw err;
    }
    return session;
  }

  async delete(state: GregaleSandboxSessionState): Promise<void> {
    this.#checkState(state);
    await this.#session(state, this.#defaults).delete();
  }

  /** Only the session's identity: its token is re-minted on resume. */
  async serializeSessionState(state: GregaleSandboxSessionState): Promise<Record<string, unknown>> {
    return { sandboxId: state.sandboxId, apiBase: state.apiBase };
  }

  async deserializeSessionState(state: Record<string, unknown>): Promise<GregaleSandboxSessionState> {
    const manifest = state.manifest instanceof Manifest ? state.manifest : new Manifest((state.manifest ?? {}) as ConstructorParameters<typeof Manifest>[0]);
    const out: GregaleSandboxSessionState = {
      backendId: 'gregale',
      sandboxId: String(state.sandboxId ?? ''),
      apiBase: String(state.apiBase ?? ''),
      manifest,
      workspaceReady: state.workspaceReady !== false,
    };
    this.#checkState(out);
    return out;
  }

  validateSessionStateForResume(input: SandboxSessionResumeValidationInput<GregaleSandboxSessionState>): void {
    const state = input.state as Partial<GregaleSandboxSessionState>;
    this.#checkState({ sandboxId: String(state.sandboxId ?? ''), apiBase: String(state.apiBase ?? '') });
  }

  /**
   * A serialized state may come from anywhere: it names a session, never
   * where the API key goes. A state for another API origin is refused.
   */
  #checkState(state: Pick<GregaleSandboxSessionState, 'sandboxId' | 'apiBase'>): void {
    if (!SANDBOX_ID.test(state.sandboxId)) {
      throw new SandboxConfigurationError('the session state names no Gregale sandbox', { sandboxId: state.sandboxId });
    }
    if (state.apiBase.replace(/\/+$/, '') !== this.#api.apiBase) {
      throw new SandboxConfigurationError(`the session state is for ${state.apiBase}, not this client's ${this.#api.apiBase}`, {
        apiBase: state.apiBase,
      });
    }
  }

  #session(state: GregaleSandboxSessionState, options: GregaleSandboxClientOptions): GregaleSandboxSession {
    const sessionOptions: GregaleSessionOptions = { portVisibility: options.portVisibility, readyTimeoutMs: options.readyTimeoutMs };
    return new GregaleSandboxSession({ state, api: this.#api, options: sessionOptions });
  }
}
