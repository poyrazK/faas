"""AI gateway helpers for Gregale apps (ADR-995).

Inside an app, ``GREGALE_OUTBOUND_URL`` reaches managed AI integrations and
routes through the outbound relay. These helpers build that base URL and
the ``X-Gregale-AI-User`` / ``X-Gregale-AI-Session`` headers that attribute
a call to one of the app's own users and keep a conversation on one pooled
provider key.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import os
import re
from collections.abc import Mapping
from urllib.parse import quote

AI_USER_HEADER = "X-Gregale-AI-User"
AI_SESSION_HEADER = "X-Gregale-AI-Session"

_AI_ID_PATTERN = re.compile(r"^[A-Za-z0-9._:@+=/|-]{1,64}$")


class AIGatewayError(ValueError):
    """A helper argument the gateway would reject. Messages never include header values."""


def is_valid_ai_user_id(value: str) -> bool:
    """Whether a value is accepted as an end-user or session ID."""
    return bool(_AI_ID_PATTERN.fullmatch(value))


def ai_gateway_url(
    *,
    integration: str | None = None,
    route: str | None = None,
    env: Mapping[str, str] | None = None,
) -> str:
    """Base URL of one integration (``/i/{id}``) or AI route (``/r/{id}``).

    Append the provider path, e.g. ``ai_gateway_url(route=route_id) + "/v1"``
    as an OpenAI client's ``base_url``.
    """
    if (integration is None) == (route is None):
        raise AIGatewayError("pass exactly one of integration or route")
    target = integration if integration is not None else route
    if not target or "/" in target:
        raise AIGatewayError("target needs one integration or route ID")
    base = (os.environ if env is None else env).get("GREGALE_OUTBOUND_URL", "")
    if not base:
        raise AIGatewayError("GREGALE_OUTBOUND_URL is not set; the relay is only available inside Gregale apps")
    kind = "i" if integration is not None else "r"
    return f"{base.rstrip('/')}/{kind}/{quote(target, safe='')}"


def ai_gateway_headers(*, user: str | None = None, session: str | None = None) -> dict[str, str]:
    """Headers naming a call's end user and conversation.

    Raises AIGatewayError for a value the gateway would reject; use
    opaque_ai_user_id for e-mail addresses and other raw identifiers.
    """
    headers: dict[str, str] = {}
    for name, value in ((AI_USER_HEADER, user), (AI_SESSION_HEADER, session)):
        if value is None:
            continue
        if not is_valid_ai_user_id(value):
            raise AIGatewayError(f"{name} must be 1 to 64 letters, digits or . _ : @ + = / | -")
        headers[name] = value
    return headers


def opaque_ai_user_id(raw: str, secret: str | bytes | None = None) -> str:
    """A stable, opaque end-user ID for a raw identifier such as an e-mail address.

    With a secret the ID is an HMAC and cannot be guessed back from the raw
    value.
    """
    data = raw.encode()
    if secret is None:
        digest = hashlib.sha256(data).digest()
    else:
        key = secret.encode() if isinstance(secret, str) else secret
        digest = hmac.new(key, data, hashlib.sha256).digest()
    return "u_" + base64.urlsafe_b64encode(digest).rstrip(b"=").decode()


__all__ = (
    "AI_SESSION_HEADER",
    "AI_USER_HEADER",
    "AIGatewayError",
    "ai_gateway_headers",
    "ai_gateway_url",
    "is_valid_ai_user_id",
    "opaque_ai_user_id",
)
