from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ReplayRetryableEventFanoutFailuresRequest")


@_attrs_define
class ReplayRetryableEventFanoutFailuresRequest:
    """Bounded app-scoped replay of failures classified as retryable."""

    event_source: str | Unset = UNSET
    """Optional exact event source filter; must be paired with event_id."""
    event_id: str | Unset = UNSET
    """Optional exact event ID filter; must be paired with event_source."""
    limit: int | Unset = 100
    """Maximum recipients to requeue in this call."""

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        limit = self.limit

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if event_source is not UNSET:
            field_dict["event_source"] = event_source
        if event_id is not UNSET:
            field_dict["event_id"] = event_id
        if limit is not UNSET:
            field_dict["limit"] = limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_source = d.pop("event_source", UNSET)

        event_id = d.pop("event_id", UNSET)

        limit = d.pop("limit", UNSET)

        replay_retryable_event_fanout_failures_request = cls(
            event_source=event_source,
            event_id=event_id,
            limit=limit,
        )

        return replay_retryable_event_fanout_failures_request
