"""AI gateway helpers for Gregale apps (ADR-995).

Inside an app, ``GREGALE_OUTBOUND_URL`` reaches managed AI integrations and
routes through the outbound relay. These helpers build that base URL; the
``X-Gregale-AI-User`` / ``X-Gregale-AI-Session`` headers that attribute a
call to one of the app's own users and keep a conversation on one pooled
provider key; the ``X-Gregale-AI-Tags`` and ``X-Gregale-AI-Trace`` headers
that tag a call and group an agent task's calls; and feedback on a call by
its ``X-Gregale-AI-Call`` ID (ADR 20261027132936). ``ai_prompt`` calls a
managed prompt by name (ADR 20261114180740).
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import re
import urllib.error
import urllib.request
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any
from urllib.parse import quote

AI_USER_HEADER = "X-Gregale-AI-User"
AI_SESSION_HEADER = "X-Gregale-AI-Session"
AI_TAGS_HEADER = "X-Gregale-AI-Tags"
AI_TRACE_HEADER = "X-Gregale-AI-Trace"
AI_CALL_HEADER = "X-Gregale-AI-Call"
AI_PROMPT_VERSION_HEADER = "X-Gregale-AI-Prompt-Version"

_AI_ID_PATTERN = re.compile(r"^[A-Za-z0-9._:@+=/|-]{1,64}$")
_AI_TAG_KEY_PATTERN = re.compile(r"^[a-z][a-z0-9_.-]{0,31}$")
_AI_TAG_VALUE_PATTERN = re.compile(r"^[A-Za-z0-9._:@+/-]{1,64}$")
_AI_FEEDBACK_LABEL_PATTERN = re.compile(r"^[a-z][a-z0-9_.-]{0,31}$")
_AI_TAGS_MAX = 5
_AI_PROMPT_NAME_PATTERN = re.compile(r"^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")


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


def ai_gateway_headers(
    *,
    user: str | None = None,
    session: str | None = None,
    tags: Mapping[str, str] | None = None,
    trace: str | None = None,
) -> dict[str, str]:
    """Headers naming a call's end user, conversation, tags and trace.

    ``tags`` holds up to five tags, such as ``{"feature": "search"}``, to see
    and cap cost by; ``trace`` names the agent task the call belongs to.
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
    if trace is not None:
        if not _AI_ID_PATTERN.fullmatch(trace):
            raise AIGatewayError(f"{AI_TRACE_HEADER} must be 1 to 64 letters, digits or . _ : @ + = / | -")
        headers[AI_TRACE_HEADER] = trace
    if tags is not None:
        if not 1 <= len(tags) <= _AI_TAGS_MAX:
            raise AIGatewayError(f"{AI_TAGS_HEADER} takes 1 to {_AI_TAGS_MAX} tags")
        for key, value in tags.items():
            if not _AI_TAG_KEY_PATTERN.fullmatch(key) or not _AI_TAG_VALUE_PATTERN.fullmatch(value):
                raise AIGatewayError(
                    f"{AI_TAGS_HEADER} keys are 1 to 32 lowercase letters, digits, _ . or - starting with a letter; "
                    "values 1 to 64 letters, digits or . _ : @ + / -"
                )
        headers[AI_TAGS_HEADER] = ",".join(f"{key}={value}" for key, value in tags.items())
    return headers


def ai_gateway_feedback(
    *,
    integration: str,
    call_id: str,
    rating: str,
    label: str | None = None,
    env: Mapping[str, str] | None = None,
    timeout: float = 10.0,
) -> None:
    """Rate one of the app's AI gateway calls (ADR 20261021171712).

    ``call_id`` is the call's ``X-Gregale-AI-Call`` response header and
    ``rating`` is ``"good"`` or ``"bad"``. The integration needs its request
    log on. A later rating of the same call replaces the earlier one.
    """
    if rating not in ("good", "bad"):
        raise AIGatewayError("rating must be good or bad")
    if label is not None and not _AI_FEEDBACK_LABEL_PATTERN.fullmatch(label):
        raise AIGatewayError("label must be 1 to 32 lowercase letters, digits, _ . or - starting with a letter")
    if not call_id:
        raise AIGatewayError("call_id is the X-Gregale-AI-Call response header")
    body: dict[str, str] = {"call_id": call_id, "rating": rating}
    if label is not None:
        body["label"] = label
    request = urllib.request.Request(
        ai_gateway_url(integration=integration, env=env) + "/_gregale/feedback",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout):  # noqa: S310 - the app's own loopback relay
            return
    except urllib.error.HTTPError as error:
        raise AIGatewayError(f"feedback was refused with status {error.code}") from None


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


@dataclass(frozen=True)
class AIPromptResult:
    """The answer of a managed prompt call."""

    completion: dict[str, Any]
    """The OpenAI chat completions response."""
    text: str
    """The first choice's message text, or ``""`` when it has none."""
    version: int
    """The version that served the call."""
    call_id: str | None
    """The call's ID, which ``ai_gateway_feedback`` names."""


class AIPrompt:
    """A managed prompt the app calls by name at ``$GREGALE_OUTBOUND_URL/p/{name}``."""

    def __init__(self, name: str) -> None:
        if not _AI_PROMPT_NAME_PATTERN.fullmatch(name):
            raise AIGatewayError("a prompt name is a DNS label: lowercase letters, digits and -")
        self.name = name

    def run(
        self,
        variables: Mapping[str, str] | None = None,
        *,
        messages: Sequence[Mapping[str, Any]] | None = None,
        version: int | None = None,
        user: str | None = None,
        session: str | None = None,
        tags: Mapping[str, str] | None = None,
        trace: str | None = None,
        env: Mapping[str, str] | None = None,
        timeout: float = 120.0,
    ) -> AIPromptResult:
        """Render the prompt with ``variables`` and return the model's answer.

        ``messages`` are OpenAI chat messages appended after the template's,
        such as the conversation so far; ``version`` pins a version instead
        of the live one.
        """
        if version is not None and (isinstance(version, bool) or not isinstance(version, int) or version < 1):
            raise AIGatewayError("version must be a positive integer")
        base = (os.environ if env is None else env).get("GREGALE_OUTBOUND_URL", "")
        if not base:
            raise AIGatewayError("GREGALE_OUTBOUND_URL is not set; the relay is only available inside Gregale apps")
        body: dict[str, Any] = {}
        if variables:
            body["variables"] = dict(variables)
        if messages:
            body["messages"] = [dict(message) for message in messages]
        if version is not None:
            body["version"] = version
        headers = {"Content-Type": "application/json"}
        headers.update(ai_gateway_headers(user=user, session=session, tags=tags, trace=trace))
        request = urllib.request.Request(
            f"{base.rstrip('/')}/p/{self.name}", data=json.dumps(body).encode(), headers=headers, method="POST"
        )
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:  # noqa: S310 - the app's own loopback relay
                completion = json.loads(response.read())
                served = response.headers.get(AI_PROMPT_VERSION_HEADER) or "0"
                call_id = response.headers.get(AI_CALL_HEADER)
        except urllib.error.HTTPError as error:
            code = ""
            try:
                problem = json.loads(error.read())
                if isinstance(problem, dict) and isinstance(problem.get("code"), str):
                    code = f" ({problem['code']})"
            except ValueError:
                pass
            raise AIGatewayError(f"prompt call was refused with status {error.code}{code}") from None
        text = ""
        choices = completion.get("choices") if isinstance(completion, dict) else None
        if isinstance(choices, list) and choices and isinstance(choices[0], dict):
            content = (choices[0].get("message") or {}).get("content")
            text = content if isinstance(content, str) else ""
        return AIPromptResult(completion=completion, text=text, version=int(served), call_id=call_id)


def ai_prompt(name: str) -> AIPrompt:
    """A managed prompt by name, such as ``ai_prompt("support-reply").run({"question": q})``."""
    return AIPrompt(name)


__all__ = (
    "AI_CALL_HEADER",
    "AI_PROMPT_VERSION_HEADER",
    "AIPrompt",
    "AIPromptResult",
    "ai_prompt",
    "AI_SESSION_HEADER",
    "AI_TAGS_HEADER",
    "AI_TRACE_HEADER",
    "AI_USER_HEADER",
    "AIGatewayError",
    "ai_gateway_feedback",
    "ai_gateway_headers",
    "ai_gateway_url",
    "is_valid_ai_user_id",
    "opaque_ai_user_id",
)
