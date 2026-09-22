from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleRetryAction")


@_attrs_define
class EdgeRuleRetryAction:
    """Request replay against a different healthy instance
    (ADR-201 §1, kind=retry).

    There is deliberately NO "retry on status" field. Only a
    TRANSPORT failure may arm a replay — a guest that answered 5xx
    has served the request, and replaying it would run the
    customer's side effects a second time — so the set of
    retryable conditions is not configurable.

    A replay additionally requires that the response has not
    committed, a healthy sibling exists, and the remaining
    `kind=budget` allowance is at least `min_remaining_ms`. A
    retry can never extend a request's deadline.

    """

    max_attempts: int | Unset = 2
    """Total attempts, NOT retries: 2 means the original plus one
    replay. 0 applies the default (2). 1 is rejected — it is a
    silent no-op that reads like protection.

    The ceiling is deliberately tight: every replay holds
    another instance's concurrency slot for the duration of the
    request, so a high attempt count multiplies load against
    your own capacity at exactly the moment instances are
    already failing.
    """
    allow_non_idempotent: bool | Unset = False
    """Opt POST and PATCH into replay. Off by default.

    This is the only field here that can cost correctness
    rather than latency: a replayed POST runs your handler's
    side effect twice unless the handler is idempotent or the
    caller sends an idempotency key. GET, HEAD, OPTIONS,
    TRACE, PUT and DELETE are replayed without this flag.
    """
    min_remaining_ms: int | Unset = 250
    """Request-budget floor below which a replay is skipped. 0
    applies the default (250ms). Below this floor a retry
    mostly converts a 502 into a 504 without improving the
    outcome.
    """
    backoff_ms: int | Unset = 0
    """Delay before a replay. Defaults to 0 because the failure
    being retried is a dead peer, not a loaded one, and the
    next instance is a different process — so waiting buys
    nothing. Raise it only when the sibling may still be
    waking.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_attempts = self.max_attempts

        allow_non_idempotent = self.allow_non_idempotent

        min_remaining_ms = self.min_remaining_ms

        backoff_ms = self.backoff_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if allow_non_idempotent is not UNSET:
            field_dict["allow_non_idempotent"] = allow_non_idempotent
        if min_remaining_ms is not UNSET:
            field_dict["min_remaining_ms"] = min_remaining_ms
        if backoff_ms is not UNSET:
            field_dict["backoff_ms"] = backoff_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_attempts = d.pop("max_attempts", UNSET)

        allow_non_idempotent = d.pop("allow_non_idempotent", UNSET)

        min_remaining_ms = d.pop("min_remaining_ms", UNSET)

        backoff_ms = d.pop("backoff_ms", UNSET)

        edge_rule_retry_action = cls(
            max_attempts=max_attempts,
            allow_non_idempotent=allow_non_idempotent,
            min_remaining_ms=min_remaining_ms,
            backoff_ms=backoff_ms,
        )

        edge_rule_retry_action.additional_properties = d
        return edge_rule_retry_action

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
