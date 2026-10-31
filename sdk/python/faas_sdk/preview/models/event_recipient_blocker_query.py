from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventRecipientBlockerQuery")


@_attrs_define
class EventRecipientBlockerQuery:
    """Event and consumer identity with the original acceptance instant for a read-only lookup."""

    source: str
    id: str
    subscription_id: str
    expected_accepted_at: str
    """Nonzero RFC3339 timestamp with its original fractional precision (up to nanoseconds)."""

    def to_dict(self) -> dict[str, Any]:
        source = self.source

        id = self.id

        subscription_id = self.subscription_id

        expected_accepted_at = self.expected_accepted_at

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source": source,
                "id": id,
                "subscription_id": subscription_id,
                "expected_accepted_at": expected_accepted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = d.pop("source")

        id = d.pop("id")

        subscription_id = d.pop("subscription_id")

        expected_accepted_at = d.pop("expected_accepted_at")

        event_recipient_blocker_query = cls(
            source=source,
            id=id,
            subscription_id=subscription_id,
            expected_accepted_at=expected_accepted_at,
        )

        return event_recipient_blocker_query
