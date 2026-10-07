from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventReplayBackfillRequest")


@_attrs_define
class EventReplayBackfillRequest:
    """Acceptance-time range for a durable subscription event backfill."""

    from_: datetime.datetime
    """Inclusive platform acceptance-time lower bound."""
    until: datetime.datetime
    """Exclusive upper bound; future values are capped at creation time. The range may not exceed 30 days."""

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from": from_,
                "until": until,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        event_replay_backfill_request = cls(
            from_=from_,
            until=until,
        )

        return event_replay_backfill_request
