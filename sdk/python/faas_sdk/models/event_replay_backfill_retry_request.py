from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventReplayBackfillRetryRequest")


@_attrs_define
class EventReplayBackfillRetryRequest:
    """Bound for the number of failed routing recipients requeued in this call."""

    limit: int | Unset = 100
    """Maximum failed routing recipients to requeue in this call."""

    def to_dict(self) -> dict[str, Any]:
        limit = self.limit

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if limit is not UNSET:
            field_dict["limit"] = limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        limit = d.pop("limit", UNSET)

        event_replay_backfill_retry_request = cls(
            limit=limit,
        )

        return event_replay_backfill_retry_request
