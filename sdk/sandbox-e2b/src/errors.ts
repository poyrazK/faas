// Errors with the names E2B-style code catches. Written for Gregale; the
// names alone are kept for compatibility.

/** Anything the sandbox or the API refused. */
export class SandboxError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SandboxError';
  }
}

/** A session, file or command that does not exist. */
export class NotFoundError extends SandboxError {
  constructor(message: string) {
    super(message);
    this.name = 'NotFoundError';
  }
}

/** A session that does not exist or has ended. */
export class SandboxNotFoundError extends NotFoundError {
  constructor(message: string) {
    super(message);
    this.name = 'SandboxNotFoundError';
  }
}

/** The API key is missing or refused. */
export class AuthenticationError extends SandboxError {
  constructor(message: string) {
    super(message);
    this.name = 'AuthenticationError';
  }
}

/** A request or a command ran out of time. */
export class TimeoutError extends SandboxError {
  constructor(message: string) {
    super(message);
    this.name = 'TimeoutError';
  }
}

/** A plan limit or rate limit was reached. */
export class RateLimitError extends SandboxError {
  constructor(message: string) {
    super(message);
    this.name = 'RateLimitError';
  }
}

/** A request the API took as invalid. */
export class InvalidArgumentError extends SandboxError {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidArgumentError';
  }
}

/**
 * A call Gregale does not support, or supports differently. The message
 * names the call and what to use instead.
 */
export class UnsupportedError extends SandboxError {
  readonly call: string;
  constructor(call: string, alternative: string) {
    super(`${call} is not supported on Gregale: ${alternative}`);
    this.name = 'UnsupportedError';
    this.call = call;
  }
}

/** The result of a command that exited with a non-zero code. */
export type CommandResult = {
  exitCode: number;
  error?: string;
  stdout: string;
  stderr: string;
};

/** A foreground command, or a waited-for background one, exited non-zero. */
export class CommandExitError extends SandboxError implements CommandResult {
  readonly #result: CommandResult;
  constructor(result: CommandResult) {
    super(result.error ?? `command exited with code ${result.exitCode}`);
    this.name = 'CommandExitError';
    this.#result = result;
  }
  get exitCode(): number {
    return this.#result.exitCode;
  }
  get error(): string | undefined {
    return this.#result.error;
  }
  get stdout(): string {
    return this.#result.stdout;
  }
  get stderr(): string {
    return this.#result.stderr;
  }
}
