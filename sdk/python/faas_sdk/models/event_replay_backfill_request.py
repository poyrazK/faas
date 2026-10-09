from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventReplayBackfillRequest")


@_attrs_define
class EventReplayBackfillRequest:
    """Acceptance-time range for a durable subscription event backfill."""

    from_: datetime.datetime
    """Inclusive platform acceptance-time lower bound."""
    until: datetime.datetime
    """Exclusive upper bound; future values are capped at creation time. The range may not exceed 30 days."""
    allow_expired: bool | Unset = False
    """Explicitly override delivery age for this replay generation or historical backfill job. Preserves
    deterministic invocation identity and manual controls."""

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        allow_expired = self.allow_expired

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from": from_,
                "until": until,
            }
        )
        if allow_expired is not UNSET:
            field_dict["allow_expired"] = allow_expired

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        allow_expired = d.pop("allow_expired", UNSET)

        event_replay_backfill_request = cls(
            from_=from_,
            until=until,
            allow_expired=allow_expired,
        )

        return event_replay_backfill_request
