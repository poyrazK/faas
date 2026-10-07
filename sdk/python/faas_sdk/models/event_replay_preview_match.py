from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_replay_preview_match_original_recipient import (
    EventReplayPreviewMatchOriginalRecipient,
    check_event_replay_preview_match_original_recipient,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventReplayPreviewMatch")


@_attrs_define
class EventReplayPreviewMatch:
    """Matching retained-event metadata and original recipient membership."""

    event_id: str
    event_source: str
    event_type: str
    accepted_at: datetime.datetime
    original_recipient: EventReplayPreviewMatchOriginalRecipient
    """Membership of the immutable original recipient snapshot; unknown indicates a legacy receipt without a
    snapshot. This does not indicate handler completion or replay eligibility."""
    receipt_url: str
    schema_version: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        event_source = self.event_source

        event_type = self.event_type

        accepted_at = self.accepted_at.isoformat()

        original_recipient: str = self.original_recipient

        receipt_url = self.receipt_url

        schema_version = self.schema_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "event_source": event_source,
                "event_type": event_type,
                "accepted_at": accepted_at,
                "original_recipient": original_recipient,
                "receipt_url": receipt_url,
            }
        )
        if schema_version is not UNSET:
            field_dict["schema_version"] = schema_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        event_type = d.pop("event_type")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        original_recipient = check_event_replay_preview_match_original_recipient(d.pop("original_recipient"))

        receipt_url = d.pop("receipt_url")

        schema_version = d.pop("schema_version", UNSET)

        event_replay_preview_match = cls(
            event_id=event_id,
            event_source=event_source,
            event_type=event_type,
            accepted_at=accepted_at,
            original_recipient=original_recipient,
            receipt_url=receipt_url,
            schema_version=schema_version,
        )

        event_replay_preview_match.additional_properties = d
        return event_replay_preview_match

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
