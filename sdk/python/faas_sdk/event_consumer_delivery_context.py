"""Diagnostic attempt context for authenticated application event deliveries."""
from __future__ import annotations

import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass

from .event_consumer_transactions import event_consumer_request_from_headers, _uuid


class EventConsumerDeliveryContextUnavailableError(ValueError):
    """The delivery has no attempt metadata; do not invent an attempt or generation."""


@dataclass(frozen=True)
class EventConsumerDeliveryContext:
    account_id: str
    app_id: str
    subscription_id: str
    acceptance_id: int
    source: str
    event_id: str
    invocation_id: str
    invocation_source: str
    attempt: int
    replay_generation: int
    replayed_from_invocation_id: str | None = None
    replay_root_invocation_id: str | None = None


def event_consumer_delivery_context_from_headers(
    headers: Mapping[str, str | Sequence[str]], body: bytes, account_id: str, app_id: str,
) -> EventConsumerDeliveryContext:
    """Authenticate delivery first. Attempt metadata is not a business deduplication key."""
    request = event_consumer_request_from_headers(headers, body, account_id, app_id)

    def read(name: str) -> str | None:
        matches = [(key, value) for key, value in headers.items() if key.lower() == name]
        if not matches:
            return None
        if len(matches) != 1:
            raise ValueError("duplicate event delivery context")
        values: list[str] = []
        for _, value in matches:
            if isinstance(value, str):
                values.append(value)
            elif isinstance(value, Sequence):
                values.extend(value)
            else:
                raise ValueError("invalid event delivery header")
        if len(values) != 1 or not isinstance(values[0], str) or not values[0]:
            raise ValueError("missing or duplicate event delivery context")
        return values[0]

    attempt_raw = read("x-faas-invocation-attempt")
    generation_raw = read("x-faas-invocation-replay-generation")
    parent_raw = read("x-faas-invocation-replayed-from")
    root_raw = read("x-faas-invocation-replay-root")
    invocation_raw = read("x-faas-invocation-id")
    invocation_source = read("x-faas-invocation-source")
    if all(value is None for value in (attempt_raw, generation_raw, parent_raw, root_raw)):
        raise EventConsumerDeliveryContextUnavailableError("event delivery attempt metadata unavailable; upgrade scheduler and gateway")
    invocation_id = _uuid(invocation_raw or "")
    if invocation_source not in ("async_invoke", "replay"):
        raise ValueError("invalid event delivery source")
    if attempt_raw is None or not re.fullmatch(r"[1-9][0-9]{0,9}", attempt_raw) or int(attempt_raw) > 2147483647:
        raise ValueError("invalid event delivery attempt")
    if generation_raw is None or not re.fullmatch(r"0|[1-9][0-9]{0,18}", generation_raw) or int(generation_raw) > 9223372036854775807:
        raise ValueError("invalid event replay generation")
    parent = None if parent_raw is None else _uuid(parent_raw)
    root = None if root_raw is None else _uuid(root_raw)
    if parent == invocation_id or (parent is not None and root == invocation_id):
        raise ValueError("cyclic event delivery lineage")
    return EventConsumerDeliveryContext(request.account_id, request.app_id, request.subscription_id, request.acceptance_id,
                                       request.source, request.event_id, invocation_id, invocation_source, int(attempt_raw),
                                       int(generation_raw), parent, root)
