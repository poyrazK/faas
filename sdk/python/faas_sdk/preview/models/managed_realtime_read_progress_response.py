from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeReadProgressResponse")


@_attrs_define
class ManagedRealtimeReadProgressResponse:
    """Principal read position with unread count and current retention bounds."""

    sequence: int
    unread: int
    oldest_sequence: int
    latest_sequence: int
    history_unavailable: bool
    updated_at: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        unread = self.unread

        oldest_sequence = self.oldest_sequence

        latest_sequence = self.latest_sequence

        history_unavailable = self.history_unavailable

        updated_at = self.updated_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "unread": unread,
                "oldest_sequence": oldest_sequence,
                "latest_sequence": latest_sequence,
                "history_unavailable": history_unavailable,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        unread = d.pop("unread")

        oldest_sequence = d.pop("oldest_sequence")

        latest_sequence = d.pop("latest_sequence")

        history_unavailable = d.pop("history_unavailable")

        updated_at = d.pop("updated_at", UNSET)

        managed_realtime_read_progress_response = cls(
            sequence=sequence,
            unread=unread,
            oldest_sequence=oldest_sequence,
            latest_sequence=latest_sequence,
            history_unavailable=history_unavailable,
            updated_at=updated_at,
        )

        managed_realtime_read_progress_response.additional_properties = d
        return managed_realtime_read_progress_response

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
