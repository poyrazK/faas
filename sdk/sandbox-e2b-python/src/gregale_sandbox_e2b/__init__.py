"""An E2B-compatible client for Gregale sandbox sessions (ADR-20261027090013 §12).

Written from E2B's public documentation; it copies no E2B code and is not
affiliated with E2B. Not published until sandbox sessions launch.
"""

from .api import DEFAULT_API_BASE, ApiError
from .commands import CommandHandle, Commands, ProcessInfo
from .errors import (
    AuthenticationException,
    CommandExitException,
    CommandResult,
    InvalidArgumentException,
    NotEnoughSpaceException,
    NotFoundException,
    RateLimitException,
    SandboxException,
    SandboxNotFoundException,
    TimeoutException,
    UnsupportedException,
)
from .filesystem import OUTSIDE_MAX_BYTES, WORKSPACE, EntryInfo, FileType, Filesystem, WriteEntry, WriteInfo
from .sandbox import Sandbox, SandboxInfo, SandboxPaginator, SandboxQuery, SandboxState, SnapshotInfo

__all__ = [
    "DEFAULT_API_BASE",
    "OUTSIDE_MAX_BYTES",
    "WORKSPACE",
    "ApiError",
    "AuthenticationException",
    "CommandExitException",
    "CommandHandle",
    "CommandResult",
    "Commands",
    "EntryInfo",
    "FileType",
    "Filesystem",
    "InvalidArgumentException",
    "NotEnoughSpaceException",
    "NotFoundException",
    "ProcessInfo",
    "RateLimitException",
    "Sandbox",
    "SandboxException",
    "SandboxInfo",
    "SandboxNotFoundException",
    "SandboxPaginator",
    "SandboxQuery",
    "SandboxState",
    "SnapshotInfo",
    "TimeoutException",
    "UnsupportedException",
    "WriteEntry",
    "WriteInfo",
]
