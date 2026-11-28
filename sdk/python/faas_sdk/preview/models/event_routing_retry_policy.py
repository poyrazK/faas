from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRoutingRetryPolicy")


@_attrs_define
class EventRoutingRetryPolicy:
    """Routing policy before invocation admission. Duration budgets include routing attempt time and scheduled retry
    delays. Admission waits add no budget cost. API replacement accepts explicit settings; manifests and CLI default
    configured policies to jitter enabled.

    """

    max_attempts: int = 12
    initial_backoff_ms: int = 5000
    max_backoff_ms: int = 300000
    """Must be at least initial_backoff_ms."""
    max_delivery_age_ms: int | Unset = 0
    """Wall-clock age from platform acceptance before routing admission. Includes pauses, capacity, backoff, and
    circuit waits. Zero disables expiry. Rejected for strictly ordered subscriptions."""
    max_retry_duration_ms: int | Unset = 0
    """Zero disables the duration bound. Another retry is scheduled only when its entire delay fits within the
    remaining budget."""
    jitter: bool | Unset = False
    """Deterministic full jitter between one millisecond and the capped exponential delay."""

    def to_dict(self) -> dict[str, Any]:
        max_attempts = self.max_attempts

        initial_backoff_ms = self.initial_backoff_ms

        max_backoff_ms = self.max_backoff_ms

        max_delivery_age_ms = self.max_delivery_age_ms

        max_retry_duration_ms = self.max_retry_duration_ms

        jitter = self.jitter

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_attempts": max_attempts,
                "initial_backoff_ms": initial_backoff_ms,
                "max_backoff_ms": max_backoff_ms,
            }
        )
        if max_delivery_age_ms is not UNSET:
            field_dict["max_delivery_age_ms"] = max_delivery_age_ms
        if max_retry_duration_ms is not UNSET:
            field_dict["max_retry_duration_ms"] = max_retry_duration_ms
        if jitter is not UNSET:
            field_dict["jitter"] = jitter

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_attempts = d.pop("max_attempts")

        initial_backoff_ms = d.pop("initial_backoff_ms")

        max_backoff_ms = d.pop("max_backoff_ms")

        max_delivery_age_ms = d.pop("max_delivery_age_ms", UNSET)

        max_retry_duration_ms = d.pop("max_retry_duration_ms", UNSET)

        jitter = d.pop("jitter", UNSET)

        event_routing_retry_policy = cls(
            max_attempts=max_attempts,
            initial_backoff_ms=initial_backoff_ms,
            max_backoff_ms=max_backoff_ms,
            max_delivery_age_ms=max_delivery_age_ms,
            max_retry_duration_ms=max_retry_duration_ms,
            jitter=jitter,
        )

        return event_routing_retry_policy
