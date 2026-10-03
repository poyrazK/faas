"""Opaque login-target values for Gregale's opt-in pre-auth observer."""

from __future__ import annotations

import hashlib
import hmac

PRE_AUTH_TARGET_HEADER = "X-Gregale-Abuse-Target"


def pre_auth_target_digest(secret: str | bytes, normalized_identifier: str) -> str:
    """Return the target value to attach to a selected failed login response.

    Pass the same identifier used for account lookup, including when the
    account does not exist. The app decides whether a response is a failure.
    Use a random, app-owned secret shared by every application replica.
    """
    if isinstance(secret, str):
        try:
            key = secret.encode("utf-8")
        except UnicodeError:
            raise ValueError("pre-auth target secret must be valid Unicode") from None
    elif isinstance(secret, bytes):
        key = secret
    else:
        raise TypeError("pre-auth target secret must be a string or bytes")
    if len(key) < 32:
        raise ValueError("pre-auth target secret must contain at least 32 bytes")
    if not isinstance(normalized_identifier, str) or not normalized_identifier:
        raise TypeError("pre-auth target identifier must be a nonempty string")
    try:
        identifier = normalized_identifier.encode("utf-8")
    except UnicodeError:
        raise ValueError("pre-auth target identifier must be valid Unicode") from None
    return hmac.new(key, identifier, hashlib.sha256).hexdigest()
