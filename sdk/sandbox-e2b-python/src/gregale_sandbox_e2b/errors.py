"""Exceptions with the names E2B-style code catches.

Written for Gregale; the names alone are kept for compatibility.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Optional


class SandboxException(Exception):
    """Anything the sandbox or the API refused."""


class NotFoundException(SandboxException):
    """A session, file or command that does not exist."""


class SandboxNotFoundException(NotFoundException):
    """A session that does not exist or has ended."""


class AuthenticationException(SandboxException):
    """The API key is missing or refused."""


class TimeoutException(SandboxException):
    """A request or a command ran out of time."""


class RateLimitException(SandboxException):
    """A plan limit or rate limit was reached."""


class InvalidArgumentException(SandboxException):
    """A request the API took as invalid."""


class NotEnoughSpaceException(SandboxException):
    """The workspace is full."""


class UnsupportedException(SandboxException):
    """A call Gregale does not support, or supports differently.

    The message names the call and what to use instead.
    """

    def __init__(self, call: str, alternative: str) -> None:
        super().__init__(f"{call} is not supported on Gregale: {alternative}")
        self.call = call


@dataclass
class CommandResult:
    """A finished command's output and exit code."""

    stdout: str
    stderr: str
    exit_code: int
    error: Optional[str] = None


class CommandExitException(SandboxException):
    """A foreground command, or a waited-for background one, exited non-zero."""

    def __init__(self, result: CommandResult) -> None:
        super().__init__(result.error or f"command exited with code {result.exit_code}")
        self.stdout = result.stdout
        self.stderr = result.stderr
        self.exit_code = result.exit_code
        self.error = result.error
