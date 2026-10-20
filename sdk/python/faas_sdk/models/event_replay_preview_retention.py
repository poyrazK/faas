from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventReplayPreviewRetention")


@_attrs_define
class EventReplayPreviewRetention:
    """Existing receipt retention information without a complete archive guarantee."""

    settled_retention_seconds: int
    """Retention duration after routing settlement (2592000 seconds); unresolved receipts may survive longer."""
    history_complete: bool
    """Always false because retained receipts are not a guaranteed complete archive."""
    earliest_retained_at: datetime.datetime | Unset = UNSET
    """Account-wide earliest surviving acceptance, independent of target, range and filter; absent when none
    survive. Does not establish coverage."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        settled_retention_seconds = self.settled_retention_seconds

        history_complete = self.history_complete

        earliest_retained_at: str | Unset = UNSET
        if not isinstance(self.earliest_retained_at, Unset):
            earliest_retained_at = self.earliest_retained_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "settled_retention_seconds": settled_retention_seconds,
                "history_complete": history_complete,
            }
        )
        if earliest_retained_at is not UNSET:
            field_dict["earliest_retained_at"] = earliest_retained_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        settled_retention_seconds = d.pop("settled_retention_seconds")

        history_complete = d.pop("history_complete")

        _earliest_retained_at = d.pop("earliest_retained_at", UNSET)
        earliest_retained_at: datetime.datetime | Unset
        if isinstance(_earliest_retained_at, Unset):
            earliest_retained_at = UNSET
        else:
            earliest_retained_at = datetime.datetime.fromisoformat(_earliest_retained_at)

        event_replay_preview_retention = cls(
            settled_retention_seconds=settled_retention_seconds,
            history_complete=history_complete,
            earliest_retained_at=earliest_retained_at,
        )

        event_replay_preview_retention.additional_properties = d
        return event_replay_preview_retention

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
