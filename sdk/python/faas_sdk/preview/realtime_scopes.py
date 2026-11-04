"""Canonical private activity channels, matching the server's UTF-8/base64url encoding."""

from __future__ import annotations

import base64
import binascii
import re

REALTIME_ACTIVITY_SCOPE_PREFIX = "__activity."
# Unicode White_Space matches Go strings.TrimSpace, including UTF-8 scope labels.
_WHITESPACE = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"


def _part(value: str, max_bytes: int) -> bytes:
    if (
        not isinstance(value, str)
        or not value
        or value.strip(_WHITESPACE) != value
        or any(character in value for character in "\x00/?#\r\n")
    ):
        raise ValueError("Invalid activity parent or scope")
    try:
        encoded = value.encode("utf-8", errors="strict")
    except UnicodeError:
        raise ValueError("Activity parent and scope require valid UTF-8") from None
    if len(encoded) > max_bytes:
        raise ValueError("Activity parent or scope exceeds its UTF-8 byte limit")
    return encoded


def realtime_activity_scope_channel(parent_channel: str, scope: str) -> str:
    """Build a private scope channel; this does not grant subscription permission."""
    parent = _part(parent_channel, 256)
    label = _part(scope, 64)
    if parent_channel.startswith(REALTIME_ACTIVITY_SCOPE_PREFIX):
        raise ValueError("An activity scope cannot have a scoped parent")
    channel = REALTIME_ACTIVITY_SCOPE_PREFIX + ".".join(
        base64.urlsafe_b64encode(value).decode("ascii").rstrip("=") for value in (parent, label)
    )
    if len(channel) > 256:
        raise ValueError("Encoded activity channel exceeds 256 bytes")
    return channel


def parse_realtime_activity_scope(channel: str) -> tuple[str, str] | None:
    """Return parent/scope for a canonical reserved channel, or None for an ordinary channel.

    Malformed reserved channels raise ValueError, including padding, alternate
    encodings, invalid UTF-8 and noncanonical trailing base64 bits.
    """
    if not isinstance(channel, str):
        raise ValueError("Channel must be a string")
    if not channel.startswith(REALTIME_ACTIVITY_SCOPE_PREFIX):
        return None
    if len(channel) > 256:
        raise ValueError("Encoded activity channel exceeds 256 bytes")
    parts = channel[len(REALTIME_ACTIVITY_SCOPE_PREFIX) :].split(".")
    if len(parts) != 2 or any(re.fullmatch(r"[A-Za-z0-9_-]+", part) is None for part in parts):
        raise ValueError("Invalid activity scope channel")
    try:
        parent, scope = (
            base64.b64decode(part + "=" * (-len(part) % 4), altchars=b"-_", validate=True).decode("utf-8")
            for part in parts
        )
    except (ValueError, binascii.Error, UnicodeError):
        raise ValueError("Invalid activity scope encoding") from None
    if realtime_activity_scope_channel(parent, scope) != channel:
        raise ValueError("Noncanonical activity scope channel")
    return parent, scope
